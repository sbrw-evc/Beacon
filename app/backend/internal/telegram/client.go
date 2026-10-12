// Package telegram is a small client of the Telegram Bot API: sending and editing messages
// with inline buttons, answering button presses and reading updates by long polling. Both
// backup notification and the bot built into Umbrella use it.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is the address of the Bot API.
const DefaultAPI = "https://api.telegram.org"

// chatID: a numeric chat ID (negative for groups) or the @name of a channel.
var chatID = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)

// ValidChat checks a chat address.
func ValidChat(v string) bool { return chatID.MatchString(v) }

// ErrNoToken: the bot token is not set.
var ErrNoToken = errors.New("the Telegram bot token is not set")

// Error is an answer of the Bot API that is not ok.
type Error struct {
	Status      int
	Description string
	// RetryAfter is how long Telegram asks to wait (429).
	RetryAfter time.Duration
}

func (e *Error) Error() string { return fmt.Sprintf("Telegram: %d %s", e.Status, e.Description) }

// Permanent tells a failure retrying will not fix: a 4xx other than 429 (a wrong token, a chat
// the bot is not in, a malformed request) or a client-side mistake.
func Permanent(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusTooManyRequests
	}
	var p permanent
	return errors.As(err, &p)
}

type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// Client calls the Bot API with one bot token.
type Client struct {
	HTTP  *http.Client
	API   string
	Token string
}

// New is a client of the bot; an empty api is DefaultAPI.
func New(client *http.Client, api, token string) *Client {
	if api == "" {
		api = DefaultAPI
	}
	return &Client{HTTP: client, API: strings.TrimRight(api, "/"), Token: token}
}

// Call calls a method with a JSON body and decodes its result into out (may be nil).
func (c *Client) Call(ctx context.Context, method string, body, out any) error {
	if c.Token == "" {
		return permanent{ErrNoToken}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return permanent{err}
	}
	return c.post(ctx, method, "application/json", bytes.NewReader(raw), out)
}

// post sends a request body to a method and decodes its result into out (may be nil).
func (c *Client) post(ctx context.Context, method, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.API+"/bot"+c.Token+"/"+method, body)
	if err != nil {
		return permanent{errors.New("the Telegram API address is not valid")}
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// The error carries the URL with the token in it.
		return errors.New(strings.ReplaceAll(err.Error(), c.Token, "…"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var reply struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(data, &reply)
	if resp.StatusCode/100 != 2 || !reply.OK {
		msg := reply.Description
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &Error{Status: resp.StatusCode, Description: msg, RetryAfter: time.Duration(reply.Parameters.RetryAfter) * time.Second}
	}
	if out != nil && len(reply.Result) > 0 {
		return json.Unmarshal(reply.Result, out)
	}
	return nil
}

// User is a Telegram user or bot.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	Language  string `json:"language_code"`
}

// Name is how the user is shown: @username, else the full name.
func (u User) Name() string {
	if u.Username != "" {
		return "@" + u.Username
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Private: a chat with one person.
func (c Chat) Private() bool { return c.Type == "private" }

type Message struct {
	MessageID       int       `json:"message_id"`
	From            *User     `json:"from"`
	Chat            Chat      `json:"chat"`
	Text            string    `json:"text"`
	ReplyTo         *Message  `json:"reply_to_message"`
	ReplyMarkup     *Keyboard `json:"reply_markup"`
	MessageThreadID int       `json:"message_thread_id"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID int            `json:"update_id"`
	Message  *Message       `json:"message"`
	Callback *CallbackQuery `json:"callback_query"`
}

// Keyboard is an inline keyboard: rows of buttons under a message.
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}

// Button is a button that sends Data back to the bot, or opens URL.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Me is the bot itself.
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.Call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}

// Outgoing is a message to send.
type Outgoing struct {
	Chat     string
	HTML     string
	Keyboard *Keyboard
	// ReplyTo answers that message of the chat (0: none); a reply to a message that is gone is
	// sent as a plain message.
	ReplyTo int
}

// Send sends an HTML message and returns its ID.
func (c *Client) Send(ctx context.Context, m Outgoing) (int, error) {
	body := map[string]any{"chat_id": m.Chat, "text": m.HTML, "parse_mode": "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true}}
	if m.Keyboard != nil && len(m.Keyboard.Rows) > 0 {
		body["reply_markup"] = m.Keyboard
	}
	if m.ReplyTo != 0 {
		body["reply_parameters"] = map[string]any{"message_id": m.ReplyTo, "allow_sending_without_reply": true}
	}
	var out Message
	err := c.Call(ctx, "sendMessage", body, &out)
	return out.MessageID, err
}

// SetKeyboard replaces the buttons under a message; nil removes them.
func (c *Client) SetKeyboard(ctx context.Context, chat string, messageID int, k *Keyboard) error {
	if k == nil {
		k = &Keyboard{Rows: [][]Button{}}
	}
	err := c.Call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chat, "message_id": messageID, "reply_markup": k}, nil)
	var e *Error
	if errors.As(err, &e) && strings.Contains(e.Description, "message is not modified") {
		return nil
	}
	return err
}

// Answer answers a button press: text is shown to the person who pressed it.
func (c *Client) Answer(ctx context.Context, callbackID, text string, alert bool) error {
	return c.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text, "show_alert": alert}, nil)
}

// Updates waits up to timeout for updates after offset (long polling).
func (c *Client) Updates(ctx context.Context, offset int, timeout time.Duration) ([]Update, error) {
	var out []Update
	err := c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"}}, &out)
	return out, err
}

// DropWebhook removes a webhook of the bot: Telegram gives no updates by polling while one is
// set.
func (c *Client) DropWebhook(ctx context.Context) error {
	return c.Call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

// SetCommands sets the command menu of the bot.
func (c *Client) SetCommands(ctx context.Context, lang string, cmds [][2]string) error {
	list := make([]map[string]string, 0, len(cmds))
	for _, c := range cmds {
		list = append(list, map[string]string{"command": c[0], "description": c[1]})
	}
	body := map[string]any{"commands": list}
	if lang != "" {
		body["language_code"] = lang
	}
	return c.Call(ctx, "setMyCommands", body, nil)
}

// Voice is an audio message to send: OGG/Opus goes as a voice message, any other audio as a
// file.
type Voice struct {
	Chat     string
	Audio    []byte
	Opus     bool
	FileName string
	// Caption is HTML under the audio.
	Caption  string
	Keyboard *Keyboard
}

// SendVoice uploads a voice message (sendVoice) or an audio file (sendDocument).
func (c *Client) SendVoice(ctx context.Context, v Voice) (int, error) {
	if c.Token == "" {
		return 0, permanent{ErrNoToken}
	}
	method, field := "sendVoice", "voice"
	if !v.Opus {
		method, field = "sendDocument", "document"
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", v.Chat)
	if v.Caption != "" {
		_ = mw.WriteField("caption", v.Caption)
		_ = mw.WriteField("parse_mode", "HTML")
	}
	if v.Keyboard != nil && len(v.Keyboard.Rows) > 0 {
		k, _ := json.Marshal(v.Keyboard)
		_ = mw.WriteField("reply_markup", string(k))
	}
	fw, err := mw.CreateFormFile(field, v.FileName)
	if err != nil {
		return 0, permanent{err}
	}
	_, _ = fw.Write(v.Audio)
	if err := mw.Close(); err != nil {
		return 0, permanent{err}
	}
	var out Message
	err = c.post(ctx, method, mw.FormDataContentType(), &body, &out)
	return out.MessageID, err
}

// ChatOf is the chat address of a numeric ID.
func ChatOf(id int64) string { return strconv.FormatInt(id, 10) }
