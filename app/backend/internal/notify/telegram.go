package notify

import (
	"context"
	"strconv"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
)

// ValidChat checks a Telegram chat address: a numeric chat ID or the @name of a channel.
func ValidChat(v string) bool { return telegram.ValidChat(v) }

// Callback data of the buttons under incident messages; the bot built into Umbrella answers
// them (tgbot).
const (
	CallbackAck     = "ack:"
	CallbackResolve = "res:"
)

// telegramKeyboard is the buttons under a message: with the bot on, buttons that acknowledge
// and resolve the incident in the name of whoever presses them (the bot knows them by their
// linked account) instead of the signed acknowledgement link; the links of the message open
// pages. A follow-up (final) keeps only the links that open pages.
func telegramKeyboard(bot bool, m composed) *telegram.Keyboard {
	var k telegram.Keyboard
	actions := bot && m.incident != "" && !m.final
	if actions {
		w := lang(m.locale)
		k.Rows = append(k.Rows, []telegram.Button{{Text: "✅ " + w["ack"], Data: CallbackAck + m.incident}, {Text: "✔️ " + w["resolve"], Data: CallbackResolve + m.incident}})
	}
	var row []telegram.Button
	for _, l := range m.links {
		if l.ack && (actions || m.final) {
			continue
		}
		row = append(row, telegram.Button{Text: l.title, URL: l.url})
	}
	if len(row) > 0 {
		k.Rows = append(k.Rows, row)
	}
	if len(k.Rows) == 0 {
		return nil
	}
	return &k
}

func sendTelegram(ctx context.Context, c *telegram.Client, bot bool, chat string, m composed) (string, error) {
	reply := 0
	if m.replyTo != "" {
		reply, _ = strconv.Atoi(m.replyTo)
	}
	if reply != 0 {
		// The buttons of the first message no longer apply: the incident was taken.
		_ = c.SetKeyboard(ctx, chat, reply, telegramKeyboard(bot, composed{links: m.links, final: true, locale: m.locale}))
	}
	id, err := c.Send(ctx, telegram.Outgoing{Chat: chat, HTML: m.html, Keyboard: telegramKeyboard(bot, m), ReplyTo: reply})
	if err != nil {
		if telegram.Permanent(err) {
			return "", errPermanent{err}
		}
		return "", err
	}
	return strconv.Itoa(id), nil
}
