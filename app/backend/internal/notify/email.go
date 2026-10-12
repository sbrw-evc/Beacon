package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// ValidEmail checks an address of a recipient or a sender.
func ValidEmail(v string) bool {
	a, err := mail.ParseAddress(v)
	return err == nil && a.Address == strings.TrimSpace(v) && !strings.ContainsAny(v, "\r\n")
}

// sendMail delivers one message over SMTP: STARTTLS, implicit TLS or plain, with optional
// authentication.
func sendMail(ctx context.Context, ch model.EmailChannel, password, to, subject, body string) error {
	if ch.Host == "" {
		return errors.New("the SMTP server is not set")
	}
	port := ch.Port
	if port == 0 {
		switch ch.Security {
		case model.SMTPTLS:
			port = 465
		case model.SMTPNone:
			port = 25
		default:
			port = 587
		}
	}
	addr := net.JoinHostPort(ch.Host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(ctx, DefaultSMTPTimeout)
	defer cancel()
	tlsConf := &tls.Config{ServerName: ch.Host, InsecureSkipVerify: ch.SkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // the administrator's choice for internal relays
	dialer := &net.Dialer{}
	var conn net.Conn
	var err error
	if ch.Security == model.SMTPTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConf}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, ch.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ch.Security == model.SMTPStartTLS || ch.Security == "" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsConf); err != nil {
			return err
		}
	}
	if ch.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("the SMTP server does not offer authentication")
		}
		if err := c.Auth(plainAuth{ch.Username, password}); err != nil {
			return err
		}
	}
	from, err := mail.ParseAddress(ch.From)
	if err != nil {
		return fmt.Errorf("the sender address: %w", err)
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(message(ch.From, to, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// plainAuth is PLAIN authentication. Unlike smtp.PlainAuth it allows a plain connection when
// the administrator chose one (a relay inside the network).
type plainAuth struct{ user, password string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.password), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected SMTP authentication challenge")
	}
	return nil, nil
}

func message(from, to, subject, body string) []byte {
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "base64")
	h("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String())
}
