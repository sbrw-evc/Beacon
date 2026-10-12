// Package notifytest has a fake SMTP server, a fake Telegram Bot API and a fake incoming
// webhook (Teams, Zoom) for tests.
package notifytest

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mail is a message the fake SMTP server took.
type Mail struct {
	From, To, Subject, Body string
	User, Password          string
}

// SMTP is a plain SMTP server with PLAIN authentication. Reject makes it refuse recipients
// with 550.
type SMTP struct {
	ln     net.Listener
	mu     sync.Mutex
	mails  []Mail
	Reject map[string]bool
}

func NewSMTP(t *testing.T) *SMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &SMTP{ln: ln, Reject: map[string]bool{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *SMTP) Host() string { h, _, _ := net.SplitHostPort(s.ln.Addr().String()); return h }
func (s *SMTP) Port() int {
	_, p, _ := net.SplitHostPort(s.ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

func (s *SMTP) Mails() []Mail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mail(nil), s.mails...)
}

func (s *SMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(v string) { c.Write([]byte(v + "\r\n")) }
	say("220 fake ESMTP")
	var m Mail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			p := strings.Split(string(raw), "\x00")
			if len(p) == 3 {
				m.User, m.Password = p[1], p[2]
			}
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m.From = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			to := strings.Trim(line[len("RCPT TO:"):], "<> ")
			if s.Reject[to] {
				say("550 no such user")
				continue
			}
			m.To = to
			say("250 ok")
		case cmd == "DATA":
			say("354 go on")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			if msg, err := mail.ReadMessage(strings.NewReader(b.String())); err == nil {
				m.Subject, _ = new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
				body, _ := readAll(msg)
				m.Body = body
			}
			s.mu.Lock()
			s.mails = append(s.mails, m)
			s.mu.Unlock()
			m = Mail{User: m.User, Password: m.Password}
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func readAll(msg *mail.Message) (string, error) {
	var b strings.Builder
	sc := bufio.NewScanner(msg.Body)
	for sc.Scan() {
		b.WriteString(sc.Text())
	}
	raw, err := base64.StdEncoding.DecodeString(b.String())
	if err != nil {
		return b.String(), err
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n"), nil
}

// Message is a message the fake Telegram bot was asked to send.
type Message struct {
	ID        int
	ChatID    string
	Text      string
	ParseMode string
	// Keyboard is the reply_markup as sent; ReplyTo the message it answers.
	Keyboard json.RawMessage
	ReplyTo  int
}

// Edit is a change of the buttons of a message; Answer an answer to a button press.
type Edit struct {
	ChatID   string
	ID       int
	Keyboard json.RawMessage
}

// Telegram is a fake Bot API: getMe, sendMessage, editMessageReplyMarkup, answerCallbackQuery
// and getUpdates (updates queued with Push) for the token Token. Chats in Blocked answer 403
// like a user who blocked the bot.
type Telegram struct {
	srv      *httptest.Server
	Token    string
	mu       sync.Mutex
	messages []Message
	edits    []Edit
	answers  []string
	updates  []map[string]any
	nextID   int
	voices   []Voice
	Blocked  map[string]bool
}

// Voice is an audio upload: a voice message or a document.
type Voice struct {
	ID             int
	ChatID, Method string
	FileName       string
	Audio          []byte
	Caption        string
	Keyboard       json.RawMessage
}

// Voices are the audio messages sent.
func (f *Telegram) Voices() []Voice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Voice(nil), f.voices...)
}

func NewTelegram(t *testing.T) *Telegram {
	f := &Telegram{Token: "123:bot-token", Blocked: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *Telegram) URL() string { return f.srv.URL }

func (f *Telegram) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.messages...)
}

func (f *Telegram) Edits() []Edit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Edit(nil), f.edits...)
}

func (f *Telegram) Answers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.answers...)
}

// Push queues an update (its update_id is set) for getUpdates.
func (f *Telegram) Push(u map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	u["update_id"] = 1000 + f.nextID
	f.updates = append(f.updates, u)
}

// Pending is how many queued updates were not confirmed by a later offset.
func (f *Telegram) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updates)
}

func (f *Telegram) handle(w http.ResponseWriter, r *http.Request) {
	reply := func(code int, v map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	if !strings.HasPrefix(r.URL.Path, "/bot"+f.Token+"/") {
		reply(http.StatusUnauthorized, map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/bot"+f.Token+"/") {
	case "getMe":
		reply(http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"id": 123, "is_bot": true, "username": "umbrella_test_bot"}})
	case "sendMessage":
		var in struct {
			ChatID    json.RawMessage `json:"chat_id"`
			Text      string          `json:"text"`
			ParseMode string          `json:"parse_mode"`
			Markup    json.RawMessage `json:"reply_markup"`
			Reply     struct {
				MessageID int `json:"message_id"`
			} `json:"reply_parameters"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		chat := strings.Trim(string(in.ChatID), `"`)
		if f.Blocked[chat] {
			reply(http.StatusForbidden, map[string]any{"ok": false, "error_code": 403, "description": "Forbidden: bot was blocked by the user"})
			return
		}
		f.mu.Lock()
		id := len(f.messages) + 1
		f.messages = append(f.messages, Message{ID: id, ChatID: chat, Text: in.Text, ParseMode: in.ParseMode, Keyboard: in.Markup, ReplyTo: in.Reply.MessageID})
		f.mu.Unlock()
		reply(http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"message_id": id}})
	case "editMessageReplyMarkup":
		var in struct {
			ChatID    json.RawMessage `json:"chat_id"`
			MessageID int             `json:"message_id"`
			Markup    json.RawMessage `json:"reply_markup"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.edits = append(f.edits, Edit{ChatID: strings.Trim(string(in.ChatID), `"`), ID: in.MessageID, Keyboard: in.Markup})
		f.mu.Unlock()
		reply(http.StatusOK, map[string]any{"ok": true, "result": true})
	case "answerCallbackQuery":
		var in struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.answers = append(f.answers, in.Text)
		f.mu.Unlock()
		reply(http.StatusOK, map[string]any{"ok": true, "result": true})
	case "sendVoice", "sendDocument":
		field := map[string]string{"sendVoice": "voice", "sendDocument": "document"}[strings.TrimPrefix(r.URL.Path, "/bot"+f.Token+"/")]
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			reply(http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: " + err.Error()})
			return
		}
		file, hdr, err := r.FormFile(field)
		if err != nil {
			reply(http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: no " + field})
			return
		}
		data, _ := io.ReadAll(file)
		chat := r.FormValue("chat_id")
		if f.Blocked[chat] {
			reply(http.StatusForbidden, map[string]any{"ok": false, "error_code": 403, "description": "Forbidden: bot was blocked by the user"})
			return
		}
		f.mu.Lock()
		id := len(f.messages) + len(f.voices) + 1
		f.voices = append(f.voices, Voice{ID: id, ChatID: chat, Method: field, FileName: hdr.Filename, Audio: data, Caption: r.FormValue("caption"), Keyboard: json.RawMessage(r.FormValue("reply_markup"))})
		f.mu.Unlock()
		reply(http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"message_id": id}})
	case "deleteWebhook", "setMyCommands":
		reply(http.StatusOK, map[string]any{"ok": true, "result": true})
	case "getUpdates":
		var in struct {
			Offset int `json:"offset"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		var out []map[string]any
		for i := 0; i < 20; i++ {
			f.mu.Lock()
			keep := f.updates[:0]
			for _, u := range f.updates {
				if u["update_id"].(int) >= in.Offset {
					keep = append(keep, u)
				}
			}
			f.updates = keep
			out = append([]map[string]any(nil), f.updates...)
			f.mu.Unlock()
			if len(out) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		reply(http.StatusOK, map[string]any{"ok": true, "result": out})
	default:
		reply(http.StatusNotFound, map[string]any{"ok": false, "error_code": 404, "description": "Not Found"})
	}
}
