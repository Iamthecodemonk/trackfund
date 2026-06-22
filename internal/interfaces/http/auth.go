package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	authapp "trackfund/internal/application/auth"
	domainauth "trackfund/internal/domain/auth"
)

type contextKey string

const userContextKey contextKey = "authUser"

type authEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var request authapp.RegisterCommand
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.Auth.Register(r.Context(), request); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, authEnvelope{
		Success: true,
		Message: "OTP sent to " + strings.ToLower(strings.TrimSpace(request.Email)),
		Data: map[string]any{
			"email":       strings.ToLower(strings.TrimSpace(request.Email)),
			"requiresOtp": true,
		},
	})
}

func (s *Server) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email string `json:"email"`
		OTP   string `json:"otp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.Auth.VerifyOTP(r.Context(), request.Email, request.OTP); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Email verified successfully. You can now log in."})
}

func (s *Server) resendOTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.Auth.ResendOTP(r.Context(), request.Email); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "OTP resent to " + strings.ToLower(strings.TrimSpace(request.Email))})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceName string `json:"deviceName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.app.Auth.Login(r.Context(), authapp.LoginCommand{
		Email:      request.Email,
		Password:   request.Password,
		DeviceName: request.DeviceName,
		IPAddress:  clientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Login successful.", Data: result})
}

func (s *Server) googleAuth(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDToken string `json:"idToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}

	// verify id token with Google
	resp, err := http.Get(fmt.Sprintf("https://oauth2.googleapis.com/tokeninfo?id_token=%s", request.IDToken))
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		writeAuthError(w, http.StatusUnauthorized, fmt.Errorf("invalid id token: %s", string(body)))
		return
	}
	var info struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		writeAuthError(w, http.StatusInternalServerError, err)
		return
	}

	// check if provider is linked to a user
	user, ok, err := s.app.Auth.FindUserByOAuth(r.Context(), "google", info.Sub)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err)
		return
	}
	if ok {
		// login existing user
		result, err := s.app.Auth.Login(r.Context(), authapp.LoginCommand{Email: user.Email, Password: ""})
		if err != nil {
			// fallback: issue tokens directly
			token, _ := s.app.Auth.CreateTokensForUser(r.Context(), user)
			writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Login successful.", Data: token})
			return
		}
		writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Login successful.", Data: result})
		return
	}

	// create or link account: if user exists by email, link; otherwise create pending user and link
	existingUser, ok2, err := s.app.Auth.FindUserByEmail(r.Context(), strings.ToLower(info.Email))
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err)
		return
	}
	if ok2 {
		// link provider
		if err := s.app.Auth.SaveOAuthProvider(r.Context(), "google", info.Sub, existingUser.ID); err != nil {
			writeAuthError(w, http.StatusInternalServerError, err)
			return
		}
		token, _ := s.app.Auth.CreateTokensForUser(r.Context(), existingUser)
		writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Login successful.", Data: token})
		return
	} // <--- THIS BRACE WAS MISSING

	// create new user and link
	newID, _ := authapp.RandomID()
	now := time.Now().UTC()
	newUser := domainauth.User{
		ID:              newID,
		FullName:        info.Name,
		Email:           strings.ToLower(info.Email),
		Phone:           "",
		PasswordHash:    "",
		Role:            domainauth.RoleUser,
		Status:          domainauth.StatusActive,
		IsEmailVerified: true,
		TokenVersion:    0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.app.Auth.CreateUser(r.Context(), newUser); err != nil {
		writeAuthError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.app.Auth.SaveOAuthProvider(r.Context(), "google", info.Sub, newUser.ID); err != nil {
		writeAuthError(w, http.StatusInternalServerError, err)
		return
	}
	token, _ := s.app.Auth.CreateTokensForUser(r.Context(), newUser)
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Login successful.", Data: token})
}

func (s *Server) refreshToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RefreshToken string `json:"refreshToken"`
		DeviceName   string `json:"deviceName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.app.Auth.Refresh(r.Context(), request.RefreshToken, request.DeviceName, clientIP(r), r.UserAgent())
	if err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Data: map[string]string{
		"accessToken":  result.AccessToken,
		"refreshToken": result.RefreshToken,
	}})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.Auth.ForgotPassword(r.Context(), request.Email); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "If an account exists, an OTP has been sent."})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email       string `json:"email"`
		OTP         string `json:"otp"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.Auth.ResetPassword(r.Context(), request.Email, request.OTP, request.NewPassword); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Password reset successful."})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	user := currentUser(r)
	if err := s.app.Auth.ChangePassword(r.Context(), user.ID, request.CurrentPassword, request.NewPassword); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Password changed successfully."})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.Auth.Logout(r.Context(), request.RefreshToken); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Logged out successfully."})
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := s.app.Auth.LogoutAll(r.Context(), user.ID); err != nil {
		writeAuthError(w, authStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, authEnvelope{Success: true, Message: "Logged out from all devices."})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			writeAuthError(w, http.StatusUnauthorized, authapp.ErrUnauthorized)
			return
		}
		token := strings.TrimSpace(header[len("Bearer "):])
		user, err := s.app.Auth.AuthenticateAccessToken(r.Context(), token)
		if err != nil {
			writeAuthError(w, authStatus(err), err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

func authorizeRoles(roles ...domainauth.Role) func(http.Handler) http.Handler {
	allowed := map[domainauth.Role]bool{}
	for _, role := range roles {
		allowed[role] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := currentUser(r)
			if !allowed[user.Role] {
				writeAuthError(w, http.StatusForbidden, authapp.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func currentUser(r *http.Request) domainauth.User {
	user, _ := r.Context().Value(userContextKey).(domainauth.User)
	return user
}

func writeAuthError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, authEnvelope{Success: false, Message: err.Error()})
}

func authStatus(err error) int {
	switch {
	case errors.Is(err, authapp.ErrInvalidCredentials):
		return http.StatusUnauthorized
	case errors.Is(err, authapp.ErrEmailNotVerified):
		return http.StatusForbidden
	case errors.Is(err, authapp.ErrAccountRestricted):
		return http.StatusForbidden
	case errors.Is(err, authapp.ErrInvalidOTP):
		return http.StatusBadRequest
	case errors.Is(err, authapp.ErrTooManyOTPRequests):
		return http.StatusTooManyRequests
	case errors.Is(err, authapp.ErrSessionExpired), errors.Is(err, authapp.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, authapp.ErrEmailExists), errors.Is(err, authapp.ErrWeakPassword):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return r.RemoteAddr
}
