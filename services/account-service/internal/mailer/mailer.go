// Package mailer provides email sending via SMTP.
// When SMTP is not configured (SMTP_HOST is empty) it falls back to logging
// the email to stdout so development still works without a mail server.
package mailer

import (
	"fmt"
	"net/smtp"
	"strings"

	"github.com/ethioscript/account-service/internal/config"
	"go.uber.org/zap"
)

// Mailer sends transactional emails.
type Mailer struct {
	cfg config.EmailConfig
	log *zap.Logger
}

// New creates a Mailer. If SMTP is not configured it operates in log-only mode.
func New(cfg config.EmailConfig, log *zap.Logger) *Mailer {
	return &Mailer{cfg: cfg, log: log}
}

// SendPasswordReset sends a password-reset email.
func (m *Mailer) SendPasswordReset(toEmail, rawToken, appBaseURL string) error {
	resetLink := fmt.Sprintf("%s/auth/reset-password?token=%s", appBaseURL, rawToken)
	subject := "Reset your EthioScript password"
	body := fmt.Sprintf(
		"Hello,\n\nClick the link below to reset your password (valid 30 minutes):\n\n%s\n\nIf you did not request this, ignore this email.\n\nEthioScript Team",
		resetLink,
	)
	return m.send(toEmail, subject, body)
}

// SendEmailVerification sends an email verification link.
func (m *Mailer) SendEmailVerification(toEmail, rawToken, appBaseURL string) error {
	verifyLink := fmt.Sprintf("%s/auth/verify-email?token=%s", appBaseURL, rawToken)
	subject := "Verify your EthioScript email address"
	body := fmt.Sprintf(
		"Hello,\n\nClick the link below to verify your email address (valid 24 hours):\n\n%s\n\nEthioScript Team",
		verifyLink,
	)
	return m.send(toEmail, subject, body)
}

// send is the internal send method with log-only fallback.
func (m *Mailer) send(to, subject, body string) error {
	if !m.cfg.Enabled {
		m.log.Info("email (SMTP not configured — log only)",
			zap.String("to", to),
			zap.String("subject", subject),
			zap.String("body_preview", body[:min(80, len(body))]),
		)
		return nil
	}

	msg := strings.Join([]string{
		fmt.Sprintf("From: %s", m.cfg.FromAddress),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	addr := fmt.Sprintf("%s:%d", m.cfg.SMTPHost, m.cfg.SMTPPort)
	var auth smtp.Auth
	if m.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", m.cfg.SMTPUser, m.cfg.SMTPPassword, m.cfg.SMTPHost)
	}

	if err := smtp.SendMail(addr, auth, m.cfg.FromAddress, []string{to}, []byte(msg)); err != nil {
		m.log.Error("smtp send failed", zap.String("to", to), zap.Error(err))
		return fmt.Errorf("smtp: %w", err)
	}

	m.log.Info("email sent", zap.String("to", to), zap.String("subject", subject))
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
