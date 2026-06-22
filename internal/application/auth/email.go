package authapp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	"trackfund/internal/config"
	"trackfund/internal/domain/auth"
)

type SMTPEmailSender struct {
	cfg config.Config
}

func NewSMTPEmailSender(cfg config.Config) SMTPEmailSender {
	return SMTPEmailSender{cfg: cfg}
}

func (s SMTPEmailSender) SendOTP(_ context.Context, email, otp string, typ auth.OTPType) error {
	host := s.cfg.SMTPHost
	port := s.cfg.SMTPPort
	if host == "" {
		return fmt.Errorf("SMTP host not configured")
	}

	from := s.cfg.SMTPFrom
	to := []string{email}
	subject := "Your TrackFund verification code"
	body := fmt.Sprintf("Your verification code is: %s", otp)

	msg := strings.Join([]string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", email),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")

	addr := host + ":" + port

	auth := smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, host)

	// try TLS connection when using port 587 or 465
	if port == "465" {
		tlsconfig := &tls.Config{
			InsecureSkipVerify: false,
			ServerName:         host,
		}
		conn, err := tls.Dial("tcp", addr, tlsconfig)
		if err != nil {
			return err
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		if err = c.Auth(auth); err != nil {
			return err
		}
		if err = c.Mail(from); err != nil {
			return err
		}
		for _, rcpt := range to {
			if err = c.Rcpt(rcpt); err != nil {
				return err
			}
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(msg))
		if err != nil {
			return err
		}
		err = w.Close()
		if err != nil {
			return err
		}
		return c.Quit()
	}

	return smtp.SendMail(addr, auth, from, to, []byte(msg))
}

// LogEmailSender kept for fallback/testing
type LogEmailSender struct{}

func (LogEmailSender) SendOTP(_ context.Context, email, otp string, typ auth.OTPType) error {
	fmt.Printf("[EMAIL] to=%s otp=%s type=%s\n", email, otp, typ)
	return nil
}
