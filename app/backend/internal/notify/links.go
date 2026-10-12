package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

var ErrBadLink = errors.New("the link is not valid or has expired")

// Links signs acknowledgement links: who got the notification and which incident it is about,
// so a click acknowledges the incident in their name without signing in.
type Links struct{ key []byte }

func NewLinks(key []byte) *Links { return &Links{key: key} }

// Recipient forms: u:<user id>, e:<address>, t:<chat id>, ms:<redacted Teams webhook>,
// zm:<redacted Zoom webhook>. A webhook URL is a secret, so its recipient is the redacted form.
func UserRecipient(id string) string       { return "u:" + id }
func EmailRecipient(addr string) string    { return "e:" + addr }
func TelegramRecipient(chat string) string { return "t:" + chat }
func TeamsRecipient(url string) string     { return "ms:" + model.RedactURL(url) }
func ZoomRecipient(url string) string      { return "zm:" + model.RedactURL(url) }

func (l *Links) mac(payload string) string {
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// Sign makes the token of a link that acknowledges alertID for recipient until exp.
func (l *Links) Sign(alertID, recipient string, exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(alertID + "\n" + recipient + "\n" + strconv.FormatInt(exp.Unix(), 10)))
	return payload + "." + l.mac(payload)
}

// Verify checks a token and returns the incident and the recipient.
func (l *Links) Verify(token string, now time.Time) (alertID, recipient string, err error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || l == nil || len(l.key) == 0 || !hmac.Equal([]byte(sig), []byte(l.mac(payload))) {
		return "", "", ErrBadLink
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", "", ErrBadLink
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 3 {
		return "", "", ErrBadLink
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() > exp {
		return "", "", ErrBadLink
	}
	return parts[0], parts[1], nil
}

// compactMAC is the length of the signature of a compact token (16 characters of base64url).
const compactMAC = 12

// SignCompact makes a short token for subject and purpose until exp, made of the characters
// Telegram allows in a start parameter (A-Z a-z 0-9 _ -, at most 64): the bot link that ties a
// Telegram account to a user.
func (l *Links) SignCompact(purpose, subject string, exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(subject + "\n" + strconv.FormatInt(exp.Unix(), 36)))
	return payload + l.compactMAC(purpose, payload)
}

func (l *Links) compactMAC(purpose, payload string) string {
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(purpose + "\n" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:compactMAC])
}

// VerifyCompact checks a token of SignCompact and returns its subject.
func (l *Links) VerifyCompact(purpose, token string, now time.Time) (string, error) {
	n := base64.RawURLEncoding.EncodedLen(compactMAC)
	if l == nil || len(l.key) == 0 || len(token) <= n {
		return "", ErrBadLink
	}
	payload, sig := token[:len(token)-n], token[len(token)-n:]
	if !hmac.Equal([]byte(sig), []byte(l.compactMAC(purpose, payload))) {
		return "", ErrBadLink
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", ErrBadLink
	}
	subject, expS, ok := strings.Cut(string(raw), "\n")
	exp, err := strconv.ParseInt(expS, 36, 64)
	if !ok || err != nil || now.Unix() > exp {
		return "", ErrBadLink
	}
	return subject, nil
}
