package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrGoalNotFound         = errors.New("goal not found")
	ErrNotificationNotFound = errors.New("notification not found")
)

type GoalRepository struct{ pool *pgxpool.Pool }
type NotificationRepository struct{ pool *pgxpool.Pool }

func NewGoalRepository(pool *pgxpool.Pool) *GoalRepository { return &GoalRepository{pool: pool} }
func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

type Goal struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	TargetIcon      string     `json:"targetIcon,omitempty"`
	TargetAmount    int64      `json:"targetAmount"`
	CurrentAmount   int64      `json:"currentAmount"`
	Currency        string     `json:"currency"`
	TargetDate      *time.Time `json:"targetDate,omitempty"`
	Status          string     `json:"status"`
	RemainingAmount int64      `json:"remainingAmount"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type CreateGoal struct {
	ID, UserID, Name, Currency, TargetIcon string
	TargetAmount, CurrentAmount            int64
	TargetDate                             *time.Time
}
type UpdateGoal struct {
	Name, Currency, TargetIcon  *string
	TargetAmount, CurrentAmount *int64
	TargetDate                  *time.Time
	ClearTargetDate             bool
}

func (r *GoalRepository) Create(ctx context.Context, in CreateGoal) (Goal, error) {
	return scanGoal(r.pool.QueryRow(ctx, `INSERT INTO goals (id,user_id,title,target_icon,target_amount,current_amount,currency,target_date,status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CASE WHEN $6 >= $5 THEN 'completed' ELSE 'active' END) RETURNING id,title,target_icon,target_amount,current_amount,currency,target_date,status,created_at,updated_at`,
		in.ID, in.UserID, in.Name, in.TargetIcon, in.TargetAmount, in.CurrentAmount, in.Currency, in.TargetDate))
}
func (r *GoalRepository) List(ctx context.Context, userID string, limit, offset int) ([]Goal, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM goals WHERE user_id=$1 AND deleted_at IS NULL`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,title,target_icon,target_amount,current_amount,currency,target_date,status,created_at,updated_at FROM goals WHERE user_id=$1 AND deleted_at IS NULL ORDER BY status ASC,target_date NULLS LAST,created_at DESC,id DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	goals := []Goal{}
	for rows.Next() {
		goal, err := scanGoal(rows)
		if err != nil {
			return nil, 0, err
		}
		goals = append(goals, goal)
	}
	return goals, total, rows.Err()
}
func (r *GoalRepository) Get(ctx context.Context, userID, id string) (Goal, error) {
	goal, err := scanGoal(r.pool.QueryRow(ctx, `SELECT id,title,target_icon,target_amount,current_amount,currency,target_date,status,created_at,updated_at FROM goals WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Goal{}, ErrGoalNotFound
	}
	return goal, err
}
func (r *GoalRepository) Update(ctx context.Context, userID, id string, in UpdateGoal) (Goal, error) {
	var date any = in.TargetDate
	if in.ClearTargetDate {
		date = nil
	}
	goal, err := scanGoal(r.pool.QueryRow(ctx, `UPDATE goals SET title=COALESCE($3,title),target_icon=COALESCE($4,target_icon),target_amount=COALESCE($5,target_amount),current_amount=COALESCE($6,current_amount),currency=COALESCE($7,currency),target_date=CASE WHEN $8 THEN NULL WHEN $9::timestamptz IS NULL THEN target_date ELSE $9::timestamptz END,status=CASE WHEN COALESCE($6,current_amount)>=COALESCE($5,target_amount) THEN 'completed' ELSE 'active' END,updated_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL RETURNING id,title,target_icon,target_amount,current_amount,currency,target_date,status,created_at,updated_at`, id, userID, in.Name, in.TargetIcon, in.TargetAmount, in.CurrentAmount, in.Currency, in.ClearTargetDate, date))
	if errors.Is(err, pgx.ErrNoRows) {
		return Goal{}, ErrGoalNotFound
	}
	return goal, err
}
func (r *GoalRepository) Delete(ctx context.Context, userID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE goals SET deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	return tag.RowsAffected() == 1, err
}

type Notification struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	GoalID    *string    `json:"goalId,omitempty"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}
type NotificationPage struct {
	Notifications []Notification `json:"notifications"`
	Pagination    Page           `json:"pagination"`
	TotalUnread   int            `json:"totalUnread"`
}
type Page struct {
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
}

func (r *NotificationRepository) CreateOnce(ctx context.Context, id, userID, dedupeKey, kind, title, message string, goalID *string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO notifications (id,user_id,dedupe_key,type,title,message,goal_id) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (user_id,dedupe_key) DO NOTHING`, id, userID, dedupeKey, kind, title, message, goalID)
	return err
}
func (r *NotificationRepository) List(ctx context.Context, userID string, unreadOnly bool, limit, offset, page int) (NotificationPage, error) {
	var total, totalUnread int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE read_at IS NULL) FROM notifications WHERE user_id=$1`, userID).Scan(&total, &totalUnread); err != nil {
		return NotificationPage{}, err
	}
	q := `SELECT id,type,title,message,goal_id,read_at,created_at FROM notifications WHERE user_id=$1`
	if unreadOnly {
		q += ` AND read_at IS NULL`
	}
	q += ` ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`
	rows, err := r.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return NotificationPage{}, err
	}
	defer rows.Close()
	result := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Message, &n.GoalID, &n.ReadAt, &n.CreatedAt); err != nil {
			return NotificationPage{}, err
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return NotificationPage{}, err
	}
	return NotificationPage{Notifications: result, Pagination: Page{Page: page, Limit: limit, Total: total, HasMore: page*limit < total}, TotalUnread: totalUnread}, nil
}
func (r *NotificationRepository) SetRead(ctx context.Context, userID, id string, read bool) (Notification, error) {
	var n Notification
	err := r.pool.QueryRow(ctx, `UPDATE notifications SET read_at=CASE WHEN $3 THEN COALESCE(read_at,NOW()) ELSE NULL END WHERE id=$1 AND user_id=$2 RETURNING id,type,title,message,goal_id,read_at,created_at`, id, userID, read).Scan(&n.ID, &n.Type, &n.Title, &n.Message, &n.GoalID, &n.ReadAt, &n.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Notification{}, ErrNotificationNotFound
	}
	return n, err
}
func (r *NotificationRepository) MarkAllRead(ctx context.Context, userID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE notifications SET read_at=NOW() WHERE user_id=$1 AND read_at IS NULL`, userID)
	return tag.RowsAffected(), err
}
func (r *NotificationRepository) CreateDueReminders(ctx context.Context, newID func() (string, error)) error {
	rows, err := r.pool.Query(ctx, `SELECT id,user_id,title,target_date FROM goals WHERE deleted_at IS NULL AND status='active' AND target_date IS NOT NULL AND target_date >= CURRENT_DATE AND target_date < CURRENT_DATE + INTERVAL '4 days'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, userID, title string
		var due time.Time
		if err := rows.Scan(&id, &userID, &title, &due); err != nil {
			return err
		}
		notificationID, err := newID()
		if err != nil {
			return err
		}
		key := "goal-due:" + id + ":" + due.UTC().Format("2006-01-02")
		days := int(time.Until(due).Hours() / 24)
		message := "This goal is due soon."
		if days <= 0 {
			message = "This goal is due today."
		}
		if err := r.CreateOnce(ctx, notificationID, userID, key, "goal_due", title, message, &id); err != nil {
			return err
		}
	}
	return rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanGoal(row scanner) (Goal, error) {
	var goal Goal
	err := row.Scan(&goal.ID, &goal.Name, &goal.TargetIcon, &goal.TargetAmount, &goal.CurrentAmount, &goal.Currency, &goal.TargetDate, &goal.Status, &goal.CreatedAt, &goal.UpdatedAt)
	goal.Name = strings.TrimSpace(goal.Name)
	goal.TargetIcon = strings.TrimSpace(goal.TargetIcon)
	goal.Currency = strings.ToUpper(goal.Currency)
	goal.RemainingAmount = goal.TargetAmount - goal.CurrentAmount
	if goal.RemainingAmount < 0 {
		goal.RemainingAmount = 0
	}
	return goal, err
}
