package notify

import (
	"context"
	"errors"
	"net/textproto"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
)

// channel is one way backup notification reaches people. Everything that differs between
// channels lives here; the rest of the package loops over channels.
type channel interface {
	// Kind is the name of the channel as it is stored (alert.Notified.Channel) and asked for.
	Kind() string
	Enabled(n model.Notify) bool
	// Valid checks an address of the channel.
	Valid(addr string) bool
	// Recipient is who an address of the channel is, for acknowledgement links.
	Recipient(addr string) string
	// Show is an address as records and logs show it: webhook URLs carry a secret and are
	// redacted.
	Show(addr string) string
	// Ready: the channel is turned on and has what it needs to send.
	Ready(n model.Notify) bool
	// Addresses of a person, a team channel, a user and the extra recipients.
	Person(p alert.Person) string
	Team(ch alert.Channel) string
	User(u model.User) string
	Extra(n model.Notify) []string
	// Secret is the reference of the secret the channel needs (empty: none); an error when the
	// channel cannot work without one.
	Secret(n model.Notify) (string, error)
	// Send delivers a composed message; secret is the resolved Secret. ref names the message
	// sent where the channel can refer to it later (the Telegram message the follow-up answers).
	Send(ctx context.Context, s *Service, n model.Notify, secret, to string, m composed) (ref string, err error)
}

// tester is a channel whose test tells more than that the message went (the bot name);
// other channels are tested by sending.
type tester interface {
	Test(ctx context.Context, s *Service, n model.Notify, secret, to string, m composed) (map[string]string, error)
}

// channels in the order addresses are collected and outcomes recorded.
var channels = []channel{emailChannel{}, telegramChannel{}, teamsChannel{}, zoomChannel{}}

func channelOf(kind string) channel {
	for _, ch := range channels {
		if ch.Kind() == kind {
			return ch
		}
	}
	return nil
}

// ErrUnknownChannel: no channel has the name.
var ErrUnknownChannel = errors.New("unknown channel")

// Channels names the channels in their order.
func Channels() []string {
	out := make([]string, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.Kind())
	}
	return out
}

// ValidAddress checks an address of a channel; false for an unknown channel.
func ValidAddress(kind, addr string) bool {
	ch := channelOf(kind)
	return ch != nil && ch.Valid(addr)
}

// ShowAddress is an address of a channel as records show it (webhook URLs redacted).
func ShowAddress(kind, addr string) string {
	if ch := channelOf(kind); ch != nil {
		return ch.Show(addr)
	}
	return addr
}

// Reaches tells whether backup notification would reach somebody: a channel that is set up has
// an extra recipient, an address of one of the users or one of the teams has it as its own
// channel.
func Reaches(n model.Notify, users []*model.User, teams []*model.Team) bool {
	for _, ch := range channels {
		if !ch.Ready(n) {
			continue
		}
		for _, addr := range ch.Extra(n) {
			if ch.Valid(addr) {
				return true
			}
		}
		for _, u := range users {
			if ch.Valid(ch.User(*u)) {
				return true
			}
		}
		for _, t := range teams {
			if ch.Valid(ch.Team(alert.TeamChannel(*t))) {
				return true
			}
		}
	}
	return false
}

// UserAddress is the address of a user in a channel (empty when it is not set or the channel
// is unknown).
func UserAddress(kind string, u model.User) string {
	if ch := channelOf(kind); ch != nil {
		return ch.User(u)
	}
	return ""
}

type emailChannel struct{}

func (emailChannel) Kind() string                          { return ChannelEmail }
func (emailChannel) Enabled(n model.Notify) bool           { return n.Email.Enabled }
func (emailChannel) Valid(addr string) bool                { return ValidEmail(addr) }
func (emailChannel) Recipient(addr string) string          { return EmailRecipient(addr) }
func (emailChannel) Show(addr string) string               { return addr }
func (emailChannel) Ready(n model.Notify) bool             { return n.Email.Enabled && n.Email.Host != "" }
func (emailChannel) Person(p alert.Person) string          { return p.Email }
func (emailChannel) Team(ch alert.Channel) string          { return ch.Email }
func (emailChannel) User(u model.User) string              { return u.Email }
func (emailChannel) Extra(n model.Notify) []string         { return n.ExtraEmails }
func (emailChannel) Secret(n model.Notify) (string, error) { return n.Email.PasswordRef, nil }

func (emailChannel) Send(ctx context.Context, _ *Service, n model.Notify, secret, to string, m composed) (string, error) {
	err := sendMail(ctx, n.Email, secret, to, m.subject, m.text)
	var smtpErr *textproto.Error
	if errors.As(err, &smtpErr) && smtpErr.Code >= 500 {
		// A permanent SMTP answer: the address or the message is refused.
		return "", errPermanent{err}
	}
	return "", err
}

type telegramChannel struct{}

func (telegramChannel) Kind() string                 { return ChannelTelegram }
func (telegramChannel) Enabled(n model.Notify) bool  { return n.Telegram.Enabled }
func (telegramChannel) Valid(addr string) bool       { return ValidChat(addr) }
func (telegramChannel) Recipient(addr string) string { return TelegramRecipient(addr) }
func (telegramChannel) Show(addr string) string      { return addr }
func (telegramChannel) Ready(n model.Notify) bool {
	return n.Telegram.Enabled && n.Telegram.TokenRef != ""
}
func (telegramChannel) Person(p alert.Person) string  { return p.Telegram }
func (telegramChannel) Team(ch alert.Channel) string  { return ch.Telegram }
func (telegramChannel) User(u model.User) string      { return u.Telegram }
func (telegramChannel) Extra(n model.Notify) []string { return n.ExtraTelegram }

func (telegramChannel) Secret(n model.Notify) (string, error) {
	if n.Telegram.TokenRef == "" {
		return "", telegram.ErrNoToken
	}
	return n.Telegram.TokenRef, nil
}

func (telegramChannel) Send(ctx context.Context, s *Service, n model.Notify, secret, to string, m composed) (string, error) {
	return sendTelegram(ctx, telegram.New(s.client, n.Telegram.APIURL, secret), n.Telegram.Bot, to, m)
}

func (c telegramChannel) Test(ctx context.Context, s *Service, n model.Notify, secret, to string, m composed) (map[string]string, error) {
	me, err := telegram.New(s.client, n.Telegram.APIURL, secret).Me(ctx)
	if err != nil {
		return nil, err
	}
	_, err = c.Send(ctx, s, n, secret, to, m)
	return map[string]string{"bot": me.Username}, err
}
