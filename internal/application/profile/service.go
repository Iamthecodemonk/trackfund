package profile

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	authapp "trackfund/internal/application/auth"
	domainauth "trackfund/internal/domain/auth"
)

type Service struct {
	repo   Repository
	mailer authapp.EmailSender
	secret string
}

func NewService(repo Repository, mailer authapp.EmailSender, secret string) *Service {
	return &Service{repo: repo, mailer: mailer, secret: secret}
}
func (s *Service) Get(ctx context.Context, userID string) (Profile, error) {
	return s.repo.Get(ctx, userID)
}
func (s *Service) limit(ctx context.Context, userID, operation string, max int, window time.Duration) error {
	allowed, err := s.repo.Allow(ctx, userID, operation, max, window)
	if err != nil {
		return err
	}
	if !allowed {
		return &Error{Status: 429, Code: "profile_rate_limited", Message: "Too many requests; try again later", RetryAfter: int(window.Seconds())}
	}
	return nil
}
func (s *Service) codeHash(userID, id, code string) string {
	h := hmac.New(sha256.New, []byte(s.secret))
	h.Write([]byte("profile-email:" + userID + ":" + id + ":" + code))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Service) Patch(ctx context.Context, userID string, body map[string]json.RawMessage) (Profile, error) {
	changes, err := ParseChanges(body)
	if err != nil {
		return Profile{}, err
	}
	if err = s.limit(ctx, userID, "profile.patch", 20, time.Minute); err != nil {
		return Profile{}, err
	}
	current, err := s.repo.Get(ctx, userID)
	if err != nil {
		return Profile{}, err
	}
	var challenge *Challenge
	var code string
	if changes.Email.Set && !strings.EqualFold(*changes.Email.Value, current.Email) {
		if s.mailer == nil {
			return Profile{}, &Error{Status: 503, Code: "email_unavailable", Message: "Email verification is not configured"}
		}
		if err = s.limit(ctx, userID, "profile.email.request", 5, time.Hour); err != nil {
			return Profile{}, err
		}
		id, err := authapp.RandomID()
		if err != nil {
			return Profile{}, err
		}
		number, err := rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			return Profile{}, err
		}
		code = fmt.Sprintf("%06d", number.Int64())
		challenge = &Challenge{ID: id, Email: *changes.Email.Value, CodeHash: s.codeHash(userID, id, code), ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	}
	result, err := s.repo.Update(ctx, userID, changes, challenge)
	if err != nil {
		return Profile{}, err
	}
	if challenge != nil && result.EmailChangeID != nil && *result.EmailChangeID == challenge.ID {
		if err := s.mailer.SendOTP(ctx, challenge.Email, code, domainauth.OTPType("email_change")); err != nil {
			// Never undo another concurrent email request. Only this challenge is removed.
			if cancelErr := s.repo.CancelChallenge(ctx, userID, challenge.ID); cancelErr != nil {
				return Profile{}, cancelErr
			}
			return Profile{}, &Error{Status: 503, Code: "verification_delivery_failed", Message: "Profile fields were saved, but the verification email could not be sent. Retry the email change."}
		}
	}
	return result, nil
}
func (s *Service) VerifyEmail(ctx context.Context, userID, challengeID, code string) (Profile, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(challengeID) || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
		return Profile{}, &Error{Status: 422, Code: "validation_failed", Message: "Enter the email-change ID and six-digit verification code", Fields: map[string]string{"code": "Use the code from your verification email", "challengeId": "Use emailChangeId from the profile response"}}
	}
	if err := s.limit(ctx, userID, "profile.email.verify", 20, time.Minute); err != nil {
		return Profile{}, err
	}
	return s.repo.Verify(ctx, userID, challengeID, s.codeHash(userID, challengeID, code))
}
