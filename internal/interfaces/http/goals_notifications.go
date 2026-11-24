package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authapp "trackfund/internal/application/auth"
	"trackfund/internal/infrastructure/postgres"
)

const maxGoalBody = 16 * 1024

type goalInput struct {
	Name            *string    `json:"name"`
	TargetIcon      *string    `json:"targetIcon"`
	TargetAmount    *int64     `json:"targetAmount"`
	CurrentAmount   *int64     `json:"currentAmount"`
	Currency        *string    `json:"currency"`
	TargetDate      *time.Time `json:"targetDate"`
	ClearTargetDate bool       `json:"clearTargetDate"`
}

func (s *Server) createGoal(w http.ResponseWriter, r *http.Request) {
	if s.app.Goals == nil {
		writeGoalError(w, http.StatusServiceUnavailable, "goals_unavailable", "Goal storage is unavailable")
		return
	}
	in, ok := decodeGoalInput(w, r)
	if !ok {
		return
	}
	if in.Name == nil || in.TargetAmount == nil || in.Currency == nil {
		writeGoalError(w, http.StatusBadRequest, "missing_fields", "name, targetAmount and currency are required")
		return
	}
	if err := validateGoal(in, true); err != nil {
		writeGoalError(w, http.StatusBadRequest, "invalid_goal", err.Error())
		return
	}
	id, err := authapp.RandomID()
	if err != nil {
		writeGoalError(w, 500, "goal_id_failed", "Could not create goal")
		return
	}
	goal, err := s.app.Goals.Create(r.Context(), postgres.CreateGoal{ID: id, UserID: currentUser(r).ID, Name: strings.TrimSpace(*in.Name), TargetIcon: optionalString(in.TargetIcon), TargetAmount: *in.TargetAmount, CurrentAmount: intValue(in.CurrentAmount), Currency: strings.ToUpper(strings.TrimSpace(*in.Currency)), TargetDate: utcOptional(in.TargetDate)})
	if err != nil {
		writeGoalError(w, 500, "goal_create_failed", "Could not save goal")
		return
	}
	s.createGoalCompletionNotification(r, goal)
	writeAPISuccess(w, http.StatusCreated, goal)
}
func (s *Server) listGoals(w http.ResponseWriter, r *http.Request) {
	if s.app.Goals == nil {
		writeGoalError(w, 503, "goals_unavailable", "Goal storage is unavailable")
		return
	}
	page, limit, ok := paginationInput(w, r, 20, 100)
	if !ok {
		return
	}
	goals, total, err := s.app.Goals.List(r.Context(), currentUser(r).ID, limit, (page-1)*limit)
	if err != nil {
		writeGoalError(w, 500, "goals_list_failed", "Could not list goals")
		return
	}
	writeAPISuccess(w, 200, map[string]any{"goals": goals, "pagination": postgres.Page{Page: page, Limit: limit, Total: total, HasMore: page*limit < total}})
}
func (s *Server) getGoal(w http.ResponseWriter, r *http.Request) {
	if s.app.Goals == nil {
		writeGoalError(w, 503, "goals_unavailable", "Goal storage is unavailable")
		return
	}
	goal, err := s.app.Goals.Get(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if errors.Is(err, postgres.ErrGoalNotFound) {
		writeGoalError(w, 404, "goal_not_found", "Goal was not found")
		return
	}
	if err != nil {
		writeGoalError(w, 500, "goal_get_failed", "Could not retrieve goal")
		return
	}
	writeAPISuccess(w, 200, goal)
}
func (s *Server) updateGoal(w http.ResponseWriter, r *http.Request) {
	if s.app.Goals == nil {
		writeGoalError(w, 503, "goals_unavailable", "Goal storage is unavailable")
		return
	}
	in, ok := decodeGoalInput(w, r)
	if !ok {
		return
	}
	if in.Name == nil && in.TargetIcon == nil && in.TargetAmount == nil && in.CurrentAmount == nil && in.Currency == nil && in.TargetDate == nil && !in.ClearTargetDate {
		writeGoalError(w, 400, "missing_fields", "Send at least one editable goal field")
		return
	}
	if in.TargetDate != nil && in.ClearTargetDate {
		writeGoalError(w, 400, "invalid_goal", "targetDate and clearTargetDate cannot be sent together")
		return
	}
	if err := validateGoal(in, false); err != nil {
		writeGoalError(w, 400, "invalid_goal", err.Error())
		return
	}
	goal, err := s.app.Goals.Update(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), postgres.UpdateGoal{Name: trimOptional(in.Name), TargetIcon: normalizeOptional(in.TargetIcon), TargetAmount: in.TargetAmount, CurrentAmount: in.CurrentAmount, Currency: normalizeCurrency(in.Currency), TargetDate: utcOptional(in.TargetDate), ClearTargetDate: in.ClearTargetDate})
	if errors.Is(err, postgres.ErrGoalNotFound) {
		writeGoalError(w, 404, "goal_not_found", "Goal was not found")
		return
	}
	if err != nil {
		writeGoalError(w, 500, "goal_update_failed", "Could not update goal")
		return
	}
	s.createGoalCompletionNotification(r, goal)
	writeAPISuccess(w, 200, goal)
}
func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request) {
	if s.app.Goals == nil {
		writeGoalError(w, 503, "goals_unavailable", "Goal storage is unavailable")
		return
	}
	deleted, err := s.app.Goals.Delete(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeGoalError(w, 500, "goal_delete_failed", "Could not delete goal")
		return
	}
	if !deleted {
		writeGoalError(w, 404, "goal_not_found", "Goal was not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) createGoalCompletionNotification(r *http.Request, goal postgres.Goal) {
	if goal.Status != "completed" || s.app.Notifications == nil {
		return
	}
	id, err := authapp.RandomID()
	if err == nil {
		_ = s.app.Notifications.CreateOnce(r.Context(), id, currentUser(r).ID, "goal-completed:"+goal.ID, "goal_completed", goal.Name, "You reached this goal.", &goal.ID)
	}
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	if s.app.Notifications == nil {
		writeGoalError(w, 503, "notifications_unavailable", "Notification storage is unavailable")
		return
	}
	unread := strings.EqualFold(r.URL.Query().Get("unreadOnly"), "true")
	page, limit, ok := paginationInput(w, r, 50, 100)
	if !ok {
		return
	}
	items, err := s.app.Notifications.List(r.Context(), currentUser(r).ID, unread, limit, (page-1)*limit, page)
	if err != nil {
		writeGoalError(w, 500, "notifications_list_failed", "Could not list notifications")
		return
	}
	writeAPISuccess(w, 200, items)
}

type notificationUpdateInput struct {
	Read *bool `json:"read"`
}

func (s *Server) updateNotification(w http.ResponseWriter, r *http.Request) {
	if s.app.Notifications == nil {
		writeGoalError(w, 503, "notifications_unavailable", "Notification storage is unavailable")
		return
	}
	var input notificationUpdateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil || input.Read == nil {
		writeGoalError(w, 400, "invalid_notification", "read must be a boolean")
		return
	}
	item, err := s.app.Notifications.SetRead(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), *input.Read)
	if errors.Is(err, postgres.ErrNotificationNotFound) {
		writeGoalError(w, 404, "notification_not_found", "Notification was not found")
		return
	}
	if err != nil {
		writeGoalError(w, 500, "notification_update_failed", "Could not update notification")
		return
	}
	writeAPISuccess(w, 200, item)
}
func (s *Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if s.app.Notifications == nil {
		writeGoalError(w, 503, "notifications_unavailable", "Notification storage is unavailable")
		return
	}
	updated, err := s.app.Notifications.MarkAllRead(r.Context(), currentUser(r).ID)
	if err != nil {
		writeGoalError(w, 500, "notification_update_failed", "Could not update notifications")
		return
	}
	writeAPISuccess(w, 200, map[string]int64{"updated": updated})
}

func decodeGoalInput(w http.ResponseWriter, r *http.Request) (goalInput, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGoalBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in goalInput
	if err := d.Decode(&in); err != nil {
		writeGoalError(w, 400, "invalid_json", "Request body must be valid goal JSON")
		return in, false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		writeGoalError(w, 400, "invalid_json", "Send a single JSON object")
		return in, false
	}
	return in, true
}
func validateGoal(in goalInput, creating bool) error {
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len([]rune(strings.TrimSpace(*in.Name))) > 160) {
		return errors.New("name must contain 1 to 160 characters")
	}
	if in.TargetIcon != nil && len([]rune(strings.TrimSpace(*in.TargetIcon))) > 64 {
		return errors.New("targetIcon must be at most 64 characters")
	}
	if in.TargetAmount != nil && *in.TargetAmount <= 0 {
		return errors.New("targetAmount must be a positive integer in minor units")
	}
	if in.CurrentAmount != nil && *in.CurrentAmount < 0 {
		return errors.New("currentAmount cannot be negative")
	}
	if in.Currency != nil {
		v := strings.ToUpper(strings.TrimSpace(*in.Currency))
		if len(v) != 3 {
			return errors.New("currency must be a three-letter code")
		}
		for _, r := range v {
			if r < 'A' || r > 'Z' {
				return errors.New("currency must be a three-letter code")
			}
		}
	}
	if in.TargetDate != nil && in.TargetDate.IsZero() {
		return errors.New("targetDate must be an RFC3339 timestamp")
	}
	if creating && in.ClearTargetDate {
		return errors.New("clearTargetDate is only valid when updating a goal")
	}
	return nil
}
func intValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
func paginationInput(w http.ResponseWriter, r *http.Request, defaultLimit, maxLimit int) (int, int, bool) {
	page, limit := 1, defaultLimit
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			writeGoalError(w, 400, "invalid_pagination", "page must be a positive integer")
			return 0, 0, false
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxLimit {
			writeGoalError(w, 400, "invalid_pagination", "limit is out of range")
			return 0, 0, false
		}
	}
	return page, limit, true
}
func writeGoalError(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(w, status, code, message, "")
}
