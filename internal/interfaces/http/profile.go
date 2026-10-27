package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	profileapp "trackfund/internal/application/profile"
)

func writeProfileError(w http.ResponseWriter, err error) {
	e := &profileapp.Error{Status: 503, Code: "profile_unavailable", Message: "Could not complete the profile operation"}
	switch {
	case errors.Is(err, profileapp.ErrNotFound):
		e = &profileapp.Error{Status: 404, Code: "profile_not_found", Message: "Profile was not found"}
	case errors.Is(err, profileapp.ErrEmailConflict):
		e = &profileapp.Error{Status: 409, Code: "email_conflict", Message: "This email is already in use", Fields: map[string]string{"email": "Choose another email address"}}
	case errors.Is(err, profileapp.ErrInvalidCode):
		e = &profileapp.Error{Status: 422, Code: "invalid_email_code", Message: "The verification code is invalid or expired", Fields: map[string]string{"code": "Request a new code or check the latest email"}}
	case errors.Is(err, profileapp.ErrAttemptsExhausted):
		e = &profileapp.Error{Status: 429, Code: "email_attempts_exhausted", Message: "Too many code attempts. Request a new verification email.", RetryAfter: 600}
	default:
		var custom *profileapp.Error
		if errors.As(err, &custom) {
			e = custom
		}
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	body := map[string]any{"success": false, "message": e.Message, "code": e.Code}
	if len(e.Fields) > 0 {
		body["errors"] = e.Fields
	}
	writeJSON(w, e.Status, body)
}
func (s *Server) requireProfileAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			writeProfileError(w, &profileapp.Error{Status: 401, Code: "unauthorized", Message: "Authentication is required"})
			return
		}
		user, err := s.app.Auth.AuthenticateAccessToken(r.Context(), strings.TrimSpace(header[7:]))
		if err != nil {
			status := authStatus(err)
			if status >= 500 {
				status = 503
			}
			code := "unauthorized"
			message := "Your session is invalid or expired"
			if status == 503 {
				code = "authentication_unavailable"
				message = "Authentication is temporarily unavailable"
			}
			writeProfileError(w, &profileapp.Error{Status: status, Code: code, Message: message})
			return
		}
		if s.app.Profile == nil {
			writeProfileError(w, &profileapp.Error{Status: 503, Code: "profile_unavailable", Message: "Profile service is not configured"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}
func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.app.Profile.Get(r.Context(), currentUser(r).ID)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	writeAPISuccess(w, http.StatusOK, profile)
}
func decodeProfileBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(destination); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeProfileError(w, &profileapp.Error{Status: 413, Code: "request_too_large", Message: "Profile request exceeds 8 KiB"})
		} else {
			writeProfileError(w, &profileapp.Error{Status: 400, Code: "invalid_json", Message: "Send a JSON object"})
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeProfileError(w, &profileapp.Error{Status: 413, Code: "request_too_large", Message: "Profile request exceeds 8 KiB"})
			return false
		}
		writeProfileError(w, &profileapp.Error{Status: 400, Code: "invalid_json", Message: "Send one JSON object"})
		return false
	}
	return true
}
func (s *Server) patchProfile(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !decodeProfileBody(w, r, &body) {
		return
	}
	if body == nil {
		writeProfileError(w, &profileapp.Error{Status: 400, Code: "invalid_json", Message: "Send a JSON object"})
		return
	}
	profile, err := s.app.Profile.Patch(r.Context(), currentUser(r).ID, body)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	writeAPISuccess(w, http.StatusOK, profile)
}
func (s *Server) verifyProfileEmail(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !decodeProfileBody(w, r, &body) {
		return
	}
	if body == nil {
		writeProfileError(w, &profileapp.Error{Status: 400, Code: "invalid_json", Message: "Send a JSON object"})
		return
	}
	var id, code string
	fields := map[string]string{}
	for field, value := range body {
		switch field {
		case "challengeId":
			if json.Unmarshal(value, &id) != nil {
				fields[field] = "Must be a string"
			}
		case "code":
			if json.Unmarshal(value, &code) != nil {
				fields[field] = "Must be a string"
			}
		default:
			fields[field] = "This field is not allowed"
		}
	}
	if len(fields) > 0 {
		writeProfileError(w, &profileapp.Error{Status: 422, Code: "validation_failed", Message: "Check the verification fields", Fields: fields})
		return
	}
	profile, err := s.app.Profile.VerifyEmail(r.Context(), currentUser(r).ID, id, code)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": profile, "signInRequired": true})
}
