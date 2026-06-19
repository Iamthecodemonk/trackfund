package authapp

import (
	"context"
	"log"

	"trackfund/internal/domain/auth"
)

type LogEmailSender struct{}

func (LogEmailSender) SendOTP(_ context.Context, email, otp string, typ auth.OTPType) error {
	log.Printf("auth otp email=%s type=%s otp=%s", email, typ, otp)
	return nil
}
