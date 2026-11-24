package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authapp "trackfund/internal/application/auth"
	"trackfund/internal/infrastructure/postgres"
)

const maxGoalBody = 16 * 1024

type goalInput struct {
	Title           *string    `json:"title"`
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
	if in.Title == nil || in.TargetAmount == nil || in.Currency == nil {
		writeGoalError(w, http.StatusBadRequest, "missing_fields", "title, targetAmount and currency are required")
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
	goal, err := s.app.Goals.Create(r.Context(), postgres.CreateGoal{ID: id, UserID: currentUser(r).ID, Title: strings.TrimSpace(*in.Title), TargetAmount: *in.TargetAmount, CurrentAmount: intValue(in.CurrentAmount), Currency: strings.ToUpper(strings.TrimSpace(*in.Currency)), TargetDate: utcOptional(in.TargetDate)})
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
	goals, err := s.app.Goals.List(r.Context(), currentUser(r).ID)
	if err != nil {
		writeGoalError(w, 500, "goals_list_failed", "Could not list goals")
		return
	}
	writeAPISuccess(w, 200, goals)
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
	if in.Title == nil && in.TargetAmount == nil && in.CurrentAmount == nil && in.Currency == nil && in.TargetDate == nil && !in.ClearTargetDate {
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
	goal, err := s.app.Goals.Update(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), postgres.UpdateGoal{Title: trimOptional(in.Title), TargetAmount: in.TargetAmount, CurrentAmount: in.CurrentAmount, Currency: normalizeCurrency(in.Currency), TargetDate: utcOptional(in.TargetDate), ClearTargetDate: in.ClearTargetDate})
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
		_ = s.app.Notifications.CreateOnce(r.Context(), id, currentUser(r).ID, "goal-completed:"+goal.ID, "goal_completed", goal.Title, "You reached this goal.", &goal.ID)
	}
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	if s.app.Notifications == nil {
		writeGoalError(w, 503, "notifications_unavailable", "Notification storage is unavailable")
		return
	}
	unread := strings.EqualFold(r.URL.Query().Get("unreadOnly"), "true")
	items, err := s.app.Notifications.List(r.Context(), currentUser(r).ID, unread)
	if err != nil {
		writeGoalError(w, 500, "notifications_list_failed", "Could not list notifications")
		return
	}
	writeAPISuccess(w, 200, items)
}
func (s *Server) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	if s.app.Notifications == nil {
		writeGoalError(w, 503, "notifications_unavailable", "Notification storage is unavailable")
		return
	}
	item, err := s.app.Notifications.MarkRead(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if errors.Is(err, postgres.ErrGoalNotFound) {
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
	if in.Title != nil && (strings.TrimSpace(*in.Title) == "" || len([]rune(strings.TrimSpace(*in.Title))) > 160) {
		return errors.New("title must contain 1 to 160 characters")
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
func writeGoalError(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(w, status, code, message, "")
}
