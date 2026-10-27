package profile

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrNotFound = errors.New("profile_not_found")
var ErrEmailConflict = errors.New("email_conflict")
var ErrInvalidCode = errors.New("invalid_email_code")
var ErrAttemptsExhausted = errors.New("email_attempts_exhausted")

type Error struct {
	Status        int
	Code, Message string
	Fields        map[string]string
	RetryAfter    int
}

func (e *Error) Error() string { return e.Message }

type Profile struct {
	ID                   string     `json:"id"`
	FullName             string     `json:"fullName"`
	Email                string     `json:"email"`
	PhoneNumber          string     `json:"phoneNumber"`
	PhotoURL             *string    `json:"photoUrl"`
	PendingEmail         *string    `json:"pendingEmail"`
	EmailChangeID        *string    `json:"emailChangeId"`
	EmailChangeExpiresAt *time.Time `json:"emailChangeExpiresAt"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}
type OptionalString struct {
	Set   bool
	Value *string
}
type Changes struct{ FullName, Email, PhoneNumber, PhotoURL OptionalString }

func (c Changes) Fields() []string {
	var fields []string
	for _, f := range []struct {
		name string
		set  bool
	}{{"fullName", c.FullName.Set}, {"email", c.Email.Set}, {"phoneNumber", c.PhoneNumber.Set}, {"photoUrl", c.PhotoURL.Set}} {
		if f.set {
			fields = append(fields, f.name)
		}
	}
	return fields
}

type Challenge struct {
	ID, Email, CodeHash string
	ExpiresAt           time.Time
}
type Repository interface {
	Get(context.Context, string) (Profile, error)
	Allow(context.Context, string, string, int, time.Duration) (bool, error)
	Update(context.Context, string, Changes, *Challenge) (Profile, error)
	Verify(context.Context, string, string, string) (Profile, error)
	CancelChallenge(context.Context, string, string) error
}

func ParseChanges(body map[string]json.RawMessage) (Changes, error) {
	var changes Changes
	fields := map[string]string{}
	if len(body) == 0 {
		return changes, &Error{Status: 422, Code: "validation_failed", Message: "Provide at least one profile field", Fields: map[string]string{"body": "Provide at least one field"}}
	}
	for name, raw := range body {
		var field *OptionalString
		switch name {
		case "fullName":
			field = &changes.FullName
		case "email":
			field = &changes.Email
		case "phoneNumber":
			field = &changes.PhoneNumber
		case "photoUrl":
			field = &changes.PhotoURL
		default:
			fields[name] = "This field cannot be changed"
			continue
		}
		field.Set = true
		if string(raw) == "null" {
			if name == "phoneNumber" || name == "photoUrl" {
				continue
			}
			fields[name] = "Must be a string"
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			fields[name] = "Must be a string"
			continue
		}
		original := value
		value = strings.TrimSpace(value)
		switch name {
		case "fullName":
			if utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 120 || strings.IndexFunc(original, unicode.IsControl) >= 0 {
				fields[name] = "Enter a name between 1 and 120 characters without control characters"
			}
		case "email":
			value = strings.ToLower(value)
			address, err := mail.ParseAddress(value)
			if err != nil || address.Address != value || len(value) > 254 || !strings.Contains(strings.Split(value, "@")[len(strings.Split(value, "@"))-1], ".") {
				fields[name] = "Enter a valid email address"
			}
		case "phoneNumber":
			if strings.IndexFunc(original, unicode.IsControl) >= 0 {
				fields[name] = "Enter an international phone number"
				break
			}
			value = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(value)
			if !regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`).MatchString(value) {
				fields[name] = "Include the country code, for example +2348012345678"
			}
		case "photoUrl":
			u, err := url.Parse(value)
			if err != nil || len(value) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
				fields[name] = "Enter an HTTPS image URL without credentials or fragment, or null"
			}
		}
		field.Value = &value
	}
	if len(fields) > 0 {
		return changes, &Error{Status: 422, Code: "validation_failed", Message: "Check the highlighted profile fields", Fields: fields}
	}
	return changes, nil
}
