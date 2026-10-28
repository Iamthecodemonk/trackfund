package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"trackfund/internal/infrastructure/telemetry"

	"github.com/jackc/pgx/v5"
	authapp "trackfund/internal/application/auth"
	monoapp "trackfund/internal/application/mono"
)

func (r *MonoRepository) EnqueueWebhook(ctx context.Context, e monoapp.Event, body []byte) error {
	// Canonicalize JSON so retries with different whitespace produce the same digest.
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return monoapp.ErrInvalidEvent
	}
	canonical, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	tag, err := r.pool.Exec(ctx, `INSERT INTO mono_webhook_events(event_id,event_type,payload,payload_hash) VALUES ($1,$2,$3,$4)
 ON CONFLICT(event_id) DO UPDATE SET event_id=EXCLUDED.event_id WHERE mono_webhook_events.payload_hash=EXCLUDED.payload_hash`, e.ID, e.Type, canonical, hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return monoapp.ErrEventConflict
	}
	return nil
}

// ProcessNext locks one inbox row; a savepoint keeps all business writes atomic.
// A crash rolls back the row lock and writes, leaving the event available after restart.
func (r *MonoRepository) ProcessNextWebhook(ctx context.Context) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id string
	var body []byte
	err = tx.QueryRow(ctx, `SELECT event_id,payload FROM mono_webhook_events WHERE status='pending' AND next_attempt_at<=NOW()
 ORDER BY received_at,event_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "SAVEPOINT event_work"); err != nil {
		return false, err
	}
	e, workErr := monoapp.Decode(body)
	beforeState, afterState := "", ""
	if update, parseErr := e.AccountUpdate(); parseErr == nil && !e.IsTransactions() {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "mono-account:"+update.Account.ID); err != nil {
			return false, err
		}
		beforeState, err = webhookAccountState(ctx, tx, update.Account.ID)
		if err != nil {
			return false, err
		}
	}
	if workErr == nil {
		workErr = r.applyWebhook(ctx, tx, e)
	}
	if workErr != nil {
		if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT event_work"); err != nil {
			return false, err
		}
		code := "processing_failed"
		permanent := false
		switch {
		case errors.Is(workErr, monoapp.ErrOwnershipConflict):
			code = "ownership_conflict"
			permanent = true
		case errors.Is(workErr, monoapp.ErrInvalidEvent):
			code = "invalid_payload"
			permanent = true
		case errors.Is(workErr, monoapp.ErrMappingPending):
			code = "mapping_pending"
		}
		_, err = tx.Exec(ctx, `UPDATE mono_webhook_events SET attempts=attempts+1,last_error_code=$2,
 status=CASE WHEN $3 OR attempts+1>=20 THEN 'failed' ELSE 'pending' END,
 next_attempt_at=NOW()+LEAST(300,POWER(2,LEAST(attempts+1,9))) * INTERVAL '1 second' WHERE event_id=$1`, id, code, permanent)
	} else {
		_, err = tx.Exec(ctx, `UPDATE mono_webhook_events SET status='processed',attempts=attempts+1,last_error_code=NULL,processed_at=NOW() WHERE event_id=$1`, id)
	}
	if err != nil {
		return false, err
	}
	if update, parseErr := e.AccountUpdate(); parseErr == nil && !e.IsTransactions() {
		afterState, err = webhookAccountState(ctx, tx, update.Account.ID)
		if err != nil {
			return false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	code := "processed"
	if workErr != nil {
		code = "processing_failed"
		if errors.Is(workErr, monoapp.ErrOwnershipConflict) {
			code = "ownership_conflict"
		} else if errors.Is(workErr, monoapp.ErrInvalidEvent) {
			code = "invalid_payload"
		} else if errors.Is(workErr, monoapp.ErrMappingPending) {
			code = "mapping_pending"
		}
		telemetry.Inc("mono_webhook_processing_failures_total")
	}
	attrs := []any{"event_id", id, "event_type", e.Type, "result", code, "previous_state", beforeState, "new_state", afterState}
	if update, parseErr := e.AccountUpdate(); parseErr == nil {
		attrs = append(attrs, "reference", update.Meta.Ref, "mono_account_id", update.Account.ID, "data_status", update.Meta.DataStatus)
	}
	slog.InfoContext(ctx, "mono.webhook.processed", attrs...)
	return true, nil
}
func (r *MonoRepository) applyWebhook(ctx context.Context, tx pgx.Tx, e monoapp.Event) error {
	if !e.Known() {
		return nil
	}
	if e.IsTransactions() {
		return r.applyWebhookTransactions(ctx, tx, e)
	}
	update, err := e.AccountUpdate()
	if err != nil {
		return err
	}
	a := update.Account
	// Serialize account events, including competing session references for the same bank account.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "mono-account:"+a.ID); err != nil {
		return err
	}
	var userID string
	var sessionAccount *string
	var sessionState string
	if update.Meta.Ref != "" {
		err = tx.QueryRow(ctx, `SELECT user_id,mono_account_id,status FROM mono_link_sessions WHERE reference=$1 FOR UPDATE`, update.Meta.Ref).Scan(&userID, &sessionAccount, &sessionState)
		if errors.Is(err, pgx.ErrNoRows) {
			return monoapp.ErrMappingPending
		}
		if err != nil {
			return err
		}
		if sessionState == "failed" {
			return nil
		}
		if sessionAccount != nil && *sessionAccount != a.ID {
			return monoapp.ErrOwnershipConflict
		}
	}
	var existingOwner string
	var previous, deleted *time.Time
	err = tx.QueryRow(ctx, `SELECT user_id,last_event_at,deleted_at FROM mono_accounts WHERE mono_account_id=$1 FOR UPDATE`, a.ID).Scan(&existingOwner, &previous, &deleted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	exists := err == nil
	if deleted != nil {
		return nil
	} // Tombstones are permanent for this Mono authorization.
	if userID == "" {
		if !exists {
			return monoapp.ErrMappingPending
		}
		userID = existingOwner
	}
	if exists && existingOwner != userID {
		return monoapp.ErrOwnershipConflict
	}
	// Linking is independent of event order: older connected events can still bind
	// their verified session while account state remains at the newest timestamp.
	if !exists || ((e.Type != "mono.events.account_connected" && e.Type != "mono.events.account_created") && (previous == nil || e.Timestamp.After(*previous))) {
		id, err := authapp.RandomID()
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO mono_accounts(id,user_id,mono_account_id,provider,account_name,account_number,bank_code,currency,metadata,last_event_at)
  VALUES ($1,$2,$3,'mono',$4,$5,$6,$7,$8,$9)
  ON CONFLICT(mono_account_id) DO UPDATE SET
  account_name=COALESCE(NULLIF(EXCLUDED.account_name,''),mono_accounts.account_name),
  account_number=COALESCE(NULLIF(EXCLUDED.account_number,''),mono_accounts.account_number),
  bank_code=COALESCE(NULLIF(EXCLUDED.bank_code,''),mono_accounts.bank_code),
  currency=COALESCE(NULLIF(EXCLUDED.currency,''),mono_accounts.currency),
  metadata=COALESCE(mono_accounts.metadata,'{}'::jsonb)||EXCLUDED.metadata,
  last_event_at=EXCLUDED.last_event_at,updated_at=NOW()
  WHERE mono_accounts.user_id=EXCLUDED.user_id`, id, userID, a.ID, a.Name, a.AccountNumber, a.BankCode, a.Currency, update.Snapshot, e.Timestamp)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return monoapp.ErrOwnershipConflict
		}
	}
	if update.Meta.Ref != "" {
		_, err = tx.Exec(ctx, `UPDATE mono_link_sessions SET status='connected',mono_account_id=$2,updated_at=NOW() WHERE reference=$1`, update.Meta.Ref, a.ID)
	}
	return err
}
func (r *MonoRepository) applyWebhookTransactions(ctx context.Context, tx pgx.Tx, e monoapp.Event) error {
	transactions, err := e.Transactions()
	if err != nil {
		return err
	}
	for _, item := range transactions {
		var accountID string
		var deleted *time.Time
		err = tx.QueryRow(ctx, `SELECT id,deleted_at FROM mono_accounts WHERE mono_account_id=$1 FOR SHARE`, item.AccountID).Scan(&accountID, &deleted)
		if errors.Is(err, pgx.ErrNoRows) {
			return monoapp.ErrMappingPending
		}
		if err != nil {
			return err
		}
		if deleted != nil {
			continue
		}
		id, err := authapp.RandomID()
		if err != nil {
			return err
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		var savedAccount string
		err = tx.QueryRow(ctx, `INSERT INTO mono_transactions(id,mono_account_id,transaction_id,amount,currency,paid_at,description,raw)
  VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(transaction_id) DO UPDATE SET transaction_id=EXCLUDED.transaction_id
  WHERE mono_transactions.mono_account_id=EXCLUDED.mono_account_id RETURNING mono_account_id`, id, accountID, item.ID, item.Amount, item.Currency, item.PaidAt, item.Description, raw).Scan(&savedAccount)
		if errors.Is(err, pgx.ErrNoRows) {
			return monoapp.ErrOwnershipConflict
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (r *MonoRepository) GetLinkSession(ctx context.Context, reference, userID string) (monoapp.SessionStatus, bool, error) {
	result := monoapp.SessionStatus{AvailableData: []string{}, DataStatus: "PROCESSING", SyncStatus: "syncing"}
	var metadata []byte
	var synced, latest *time.Time
	err := r.pool.QueryRow(ctx, `SELECT s.reference,s.status,a.id,COALESCE(a.metadata,'{}'::jsonb),a.last_synced_at,a.last_event_at
 FROM mono_link_sessions s LEFT JOIN mono_accounts a ON a.mono_account_id=s.mono_account_id AND a.user_id=s.user_id AND a.deleted_at IS NULL
 WHERE s.reference=$1 AND s.user_id=$2`, reference, userID).Scan(&result.Reference, &result.Status, &result.AccountID, &metadata, &synced, &latest)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	var snapshot struct {
		Meta monoapp.Meta `json:"meta"`
	}
	if err = json.Unmarshal(metadata, &snapshot); err != nil {
		return result, false, fmt.Errorf("invalid account metadata")
	}
	if snapshot.Meta.DataStatus != "" {
		result.DataStatus = snapshot.Meta.DataStatus
	}
	if snapshot.Meta.RetrievedData != nil {
		result.AvailableData = snapshot.Meta.RetrievedData
	}
	result.TransactionsReady = result.Status != "failed" && monoapp.TransactionsAvailable(metadata) && synced != nil && (latest == nil || !synced.Before(*latest))
	switch {
	case result.TransactionsReady:
		result.SyncStatus = "ready"
		result.Status = "available"
	case result.DataStatus == "FAILED":
		result.SyncStatus = "failed"
		result.Status = "failed"
	case result.DataStatus == "UNAVAILABLE" || result.DataStatus == "DISCONNECTED" || (result.DataStatus == "PARTIAL" && !monoapp.TransactionsAvailable(metadata)):
		result.SyncStatus = "unavailable"
	}
	if synced != nil && result.Status != "failed" && result.DataStatus != "FAILED" && result.DataStatus != "DISCONNECTED" {
		result.Status = "available"
	}
	if result.Status == "failed" {
		result.SyncStatus = "failed"
	} else if synced == nil && result.SyncStatus == "syncing" && monoapp.TransactionsAvailable(metadata) && result.AccountID != nil {
		result.Status = "syncing"
	}
	return result, true, nil
}

func webhookAccountState(ctx context.Context, tx pgx.Tx, monoID string) (string, error) {
	var state string
	err := tx.QueryRow(ctx, "SELECT CASE WHEN deleted_at IS NOT NULL THEN 'removed' ELSE COALESCE(metadata->'meta'->>'data_status','PROCESSING') END FROM mono_accounts WHERE mono_account_id=$1", monoID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "missing", nil
	}
	return state, err
}
