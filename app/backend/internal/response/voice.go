package response

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
)

// Voice: the incident spoken to people in the language of their interface. Microsoft Teams
// rings the person and plays the text (pressing 1 acknowledges the incident). Telegram bots
// cannot place calls, so Telegram gets a voice message; Zoom has no API to place a call, so Zoom
// gets the audio in Team Chat.

const voiceSchema = `
CREATE TABLE IF NOT EXISTS voice_calls (
	id         text PRIMARY KEY,
	alert_id   text NOT NULL DEFAULT '',
	token      text NOT NULL UNIQUE,
	remote_id  text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL,
	doc        jsonb NOT NULL,
	audio      bytea,
	confirm    bytea
);
CREATE INDEX IF NOT EXISTS voice_calls_alert ON voice_calls (alert_id, created_at);
`

// States of a voice call.
const (
	VoicePlanned  = "planned"   // dry run: nothing was sent
	VoiceSent     = "sent"      // a voice message was delivered
	VoiceDialing  = "dialing"   // Teams is ringing the person
	VoiceTalking  = "talking"   // the person answered, the text is played
	VoiceDone     = "done"      // the person heard the text and the call ended
	VoiceAcked    = "acked"     // the person acknowledged the incident by pressing 1
	VoiceNoAnswer = "no_answer" // the call ended before anyone answered
	VoiceFailed   = "failed"
)

var (
	ErrVoiceOff    = errors.New("voice calls are turned off")
	ErrVoiceVia    = errors.New("unknown voice channel")
	ErrVoiceNobody = errors.New("nobody to call")
	// ErrVoiceGone: the call is unknown or over, its audio is no longer served.
	ErrVoiceGone = errors.New("the call is over")
)

// audioTTL: how long the audio of a call is served to Microsoft.
const audioTTL = 2 * time.Hour

// VoiceCall is the incident spoken to one person once.
type VoiceCall struct {
	ID      string `json:"id"`
	AlertID string `json:"alert_id"`
	// Via: teams, telegram or zoom.
	Via      string `json:"via"`
	Person   string `json:"person"`
	UserID   string `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
	// Address is the e-mail (Teams, Zoom) or the Telegram chat.
	Address string `json:"address,omitempty"`
	Locale  string `json:"locale"`
	Text    string `json:"text"`
	State   string `json:"state"`
	// Level is the escalation step (0: asked for by hand or a test).
	Level int    `json:"level,omitempty"`
	By    string `json:"by,omitempty"`
	// Voice: Telegram got a voice message (OGG/Opus), not an audio file.
	Voice    bool      `json:"voice,omitempty"`
	RemoteID string    `json:"remote_id,omitempty"`
	Error    string    `json:"error,omitempty"`
	DryRun   bool      `json:"dry_run,omitempty"`
	Test     bool      `json:"test,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	token    string
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) saveCall(ctx context.Context, c *VoiceCall, audio, confirm []byte) error {
	doc, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO voice_calls (id, alert_id, token, remote_id, created_at, doc, audio, confirm) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET remote_id = $4, doc = $6`, c.ID, c.AlertID, c.token, c.RemoteID, c.Created, doc, audio, confirm)
	return err
}

func (s *Service) updateCall(ctx context.Context, c *VoiceCall) error {
	doc, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE voice_calls SET remote_id = $2, doc = $3 WHERE id = $1`, c.ID, c.RemoteID, doc)
	return err
}

// Calls are the voice calls of an incident, the latest first.
func (s *Service) Calls(ctx context.Context, alertID string) ([]VoiceCall, error) {
	rows, err := s.db.Query(ctx, `SELECT doc FROM voice_calls WHERE alert_id = $1 ORDER BY created_at DESC, id LIMIT 200`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VoiceCall{}
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var c VoiceCall
		if json.Unmarshal(doc, &c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// Audio is the audio Microsoft plays in a call: main is the text, confirm the acknowledgement.
func (s *Service) Audio(ctx context.Context, token, kind string) ([]byte, error) {
	col := "audio"
	if kind == "confirm" {
		col = "confirm"
	}
	var data []byte
	err := s.db.QueryRow(ctx, `SELECT `+col+` FROM voice_calls WHERE token = $1 AND created_at > $2`, token, s.now().Add(-audioTTL)).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && data == nil) {
		return nil, ErrVoiceGone
	}
	return data, err
}

// sweepVoice drops the audio of old calls.
func (s *Service) sweepVoice(ctx context.Context) {
	if _, err := s.db.Exec(ctx, `UPDATE voice_calls SET audio = NULL, confirm = NULL WHERE created_at < $1 AND (audio IS NOT NULL OR confirm IS NOT NULL)`, s.now().Add(-audioTTL)); err != nil {
		slog.Warn("old voice audio not dropped", "err", err)
	}
}

// placeholder is a {name} of a voice template.
var placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)

// RenderVoice fills a voice template; unknown placeholders stay as they are.
func RenderVoice(tmpl string, args map[string]string) string {
	out := placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		if v, ok := args[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
	return strings.Join(strings.Fields(out), " ")
}

var digits = regexp.MustCompile(`\d+`)

// voiceArgs are the values of the placeholders, words of the locale of the listener.
func (v view) voiceArgs(via string, level int, set model.VoiceSettings, bot bool) map[string]string {
	a := v.a
	none := v.t("say.none")
	prio := v.st.Priority
	if prio == "" {
		prio = a.Severity
	}
	var services []string
	for _, s := range v.st.Assessment.Services {
		services = append(services, s.Name)
	}
	if len(services) == 0 {
		for _, s := range a.Route.Services {
			services = append(services, s.Name)
		}
	}
	team := v.t("say.team.none")
	if a.Route.Team != nil {
		team = a.Route.Team.Name
	}
	hint := ""
	switch {
	case via == model.VoiceTeams && set.AckDigit && alert.Active(a.Status):
		hint = v.t("say.ack.teams")
	case via == model.VoiceTelegram && bot && alert.Active(a.Status):
		hint = v.t("say.ack.telegram")
	}
	number := strings.Join(digits.FindAllString(a.ID, -1), " ")
	return map[string]string{
		"number":   cmp.Or(number, a.ID),
		"id":       a.ID,
		"priority": v.t("say." + prio),
		"title":    a.Title,
		"ci":       cmp.Or(a.CIName, none),
		"services": cmp.Or(strings.Join(services, ", "), none),
		"team":     team,
		"opened":   a.OpenedAt.In(v.tz).Format("15:04"),
		"status":   v.t(a.Status),
		"impact":   cmp.Or(v.t(v.st.Assessment.Impact), none),
		"level":    fmt.Sprint(max(level, 1)),
		"signal":   cmp.Or(a.Signal, none),
		"ack_hint": hint,
	}
}

// voiceText is what a person hears.
func (v view) voiceText(set model.VoiceSettings, via string, level int, bot bool) string {
	return RenderVoice(set.Template(v.locale), v.voiceArgs(via, level, set, bot))
}

// listener is a person with the language and time zone voice speaks to them in.
type listener struct {
	person
	username, locale string
	tz               *time.Location
}

func (sn snap) listenerOf(p person) listener {
	l := listener{person: p, locale: sn.locale, tz: sn.tz}
	if u, ok := sn.cat.Users[p.id]; ok {
		l.username = u.Username
		if model.ValidLocale(u.Locale) {
			l.locale = u.Locale
		}
		if u.Timezone != "" {
			if tz, err := time.LoadLocation(u.Timezone); err == nil {
				l.tz = tz
			}
		}
	}
	if l.locale != model.LocaleEN && l.locale != model.LocaleRU {
		l.locale = model.LocaleRU
	}
	return l
}

func (l listener) address(via string) string {
	if via == model.VoiceTelegram {
		return l.telegram
	}
	return l.email
}

// voiceJob is one voice delivery to people.
type voiceJob struct {
	sn    snap
	a     alert.Alert
	st    *State
	via   string
	level int
	by    string
	dry   bool
	test  bool
	// text replaces the text of the template (tests).
	text map[string]string
}

// speaker synthesizes the audio of a delivery once per text and format.
type speaker struct {
	tts   *TTS
	cache map[string][]byte
}

func (sp *speaker) speak(ctx context.Context, text, locale, format string) ([]byte, error) {
	key := format + "|" + locale + "|" + text
	if b, ok := sp.cache[key]; ok {
		return b, nil
	}
	b, err := sp.tts.Speak(ctx, text, locale, format)
	if err != nil {
		return nil, err
	}
	sp.cache[key] = b
	return b, nil
}

// callWAV is the text as Teams plays it.
func (sp *speaker) callWAV(ctx context.Context, text, locale string) ([]byte, error) {
	b, err := sp.speak(ctx, text, locale, FormatWAV)
	if err != nil {
		return nil, err
	}
	return toCallWAV(b)
}

// voiceMessage is the text as a Telegram voice message: OGG/Opus from the engine or ffmpeg;
// without either, a WAV file.
func (sp *speaker) voiceMessage(ctx context.Context, text, locale string) ([]byte, bool, error) {
	if sp.tts.Opus() {
		b, err := sp.speak(ctx, text, locale, FormatOGG)
		return b, true, err
	}
	wav, err := sp.callWAV(ctx, text, locale)
	if err != nil {
		return nil, false, err
	}
	ogg, err := toOpus(ctx, wav)
	if err != nil {
		if !errors.Is(err, errNoFFmpeg) {
			slog.Warn("voice message not converted to Opus", "err", err)
		}
		return wav, false, nil
	}
	return ogg, true, nil
}

// ttsClient is the speech engine of the settings.
func (s *Service) ttsClient(set model.TTSSettings) (*TTS, error) {
	key := ""
	if set.APIKeyRef != "" {
		if s.sec == nil {
			return nil, errors.New("secrets are not available")
		}
		v, err := s.sec.Resolve(set.APIKeyRef)
		if err != nil {
			return nil, fmt.Errorf("the speech server key is not available: %v", err)
		}
		key = v
	}
	return NewTTS(set, key, s.client), nil
}

// telegramClient is the Telegram bot of backup notification; bot tells whether the bot built into
// Umbrella answers buttons.
func (s *Service) telegramClient() (*telegram.Client, bool, error) {
	var tg model.TelegramChannel
	s.st.Read(func(d *store.Data) { tg = d.Settings.Alerting.Notify.Telegram })
	if !tg.Enabled || tg.TokenRef == "" {
		return nil, false, errors.New("the Telegram channel is turned off")
	}
	if s.sec == nil {
		return nil, false, errors.New("secrets are not available")
	}
	token, err := s.sec.Resolve(tg.TokenRef)
	if err != nil {
		return nil, false, fmt.Errorf("the Telegram bot token is not available: %v", err)
	}
	return telegram.New(s.client, tg.APIURL, token), tg.Bot, nil
}

// voice speaks the incident to people by one channel and records each call.
func (s *Service) voice(ctx context.Context, j voiceJob, people []person) []VoiceCall {
	set := j.sn.set.Voice
	now := s.now()
	var sp *speaker
	var spErr error
	if !j.dry {
		var t *TTS
		if t, spErr = s.ttsClient(set.TTS); spErr == nil {
			sp = &speaker{tts: t, cache: map[string][]byte{}}
		}
	}
	bot := false
	var tg *telegram.Client
	var tgErr error
	if j.via == model.VoiceTelegram && !j.dry {
		tg, bot, tgErr = s.telegramClient()
	} else if j.via == model.VoiceTelegram {
		var tgs model.TelegramChannel
		s.st.Read(func(d *store.Data) { tgs = d.Settings.Alerting.Notify.Telegram })
		bot = tgs.Bot
	}
	var out []VoiceCall
	for _, p := range people {
		l := j.sn.listenerOf(p)
		v := view{locale: l.locale, tz: l.tz, a: j.a, st: j.st}
		text := v.voiceText(set, j.via, j.level, bot)
		if t, ok := j.text[l.locale]; ok {
			text = t
		}
		c := &VoiceCall{ID: "VC-" + newToken()[:16], AlertID: j.a.ID, Via: j.via, Person: p.name, UserID: p.id, Username: l.username,
			Address: l.address(j.via), Locale: l.locale, Text: text, Level: j.level, By: j.by, DryRun: j.dry, Test: j.test,
			Created: now, Updated: now, token: newToken()}
		if j.test {
			c.AlertID = ""
		}
		var audio, confirm []byte
		var err error
		switch {
		case c.Address == "":
			err = fmt.Errorf("%s has no %s", p.name, map[string]string{model.VoiceTeams: "e-mail (Teams account)", model.VoiceZoom: "e-mail (Zoom account)", model.VoiceTelegram: "Telegram chat"}[j.via])
		case j.dry:
			c.State = VoicePlanned
		case spErr != nil:
			err = spErr
		default:
			switch j.via {
			case model.VoiceTeams:
				audio, confirm, err = s.ringTeams(ctx, j, c, v, sp)
			case model.VoiceTelegram:
				if tgErr != nil {
					err = tgErr
				} else {
					err = s.sendTelegramVoice(ctx, j, c, v, sp, tg, bot)
				}
			case model.VoiceZoom:
				err = s.sendZoomVoice(ctx, j, c, v, sp)
			default:
				err = ErrVoiceVia
			}
		}
		if err != nil {
			c.State, c.Error = VoiceFailed, err.Error()
			slog.Warn("voice call failed", "alert", j.a.ID, "via", j.via, "person", p.name, "err", err)
		}
		if err := s.saveCall(ctx, c, audio, confirm); err != nil {
			slog.Error("voice call not recorded", "alert", j.a.ID, "err", err)
		}
		if !j.test {
			args := map[string]string{"via": j.via, "person": p.name, "state": c.State, "locale": c.Locale}
			if c.Error != "" {
				args["error"] = c.Error
			}
			if j.level > 0 {
				args["level"] = fmt.Sprint(j.level)
			}
			if j.by != "" {
				args["by"] = j.by
			}
			if j.dry {
				args["dry"] = "1"
			}
			s.note(ctx, j.a.ID, "response_voice", args)
		}
		out = append(out, *c)
	}
	return out
}

func (s *Service) sendTelegramVoice(ctx context.Context, j voiceJob, c *VoiceCall, v view, sp *speaker, tg *telegram.Client, bot bool) error {
	audio, opus, err := sp.voiceMessage(ctx, c.Text, c.Locale)
	if err != nil {
		return err
	}
	name := "incident.wav"
	if opus {
		name = "incident.ogg"
	}
	caption := "🔊 <b>" + html.EscapeString(v.t("say."+cmp.Or(v.st.Priority, v.a.Severity))) + "</b> · " + html.EscapeString(v.a.ID+" · "+v.a.Title)
	var k telegram.Keyboard
	if bot && !j.test && alert.Active(v.a.Status) {
		k.Rows = append(k.Rows, []telegram.Button{{Text: "✅ " + v.t("ack"), Data: notify.CallbackAck + v.a.ID}})
	}
	if j.sn.public != "" && !j.test {
		k.Rows = append(k.Rows, []telegram.Button{{Text: v.t("umbrella"), URL: model.IncidentURL(j.sn.public, v.a.ID)}})
	}
	id, err := tg.SendVoice(ctx, telegram.Voice{Chat: c.Address, Audio: audio, Opus: opus, FileName: name, Caption: caption, Keyboard: &k})
	if err != nil {
		return err
	}
	c.State, c.Voice, c.RemoteID = VoiceSent, opus, fmt.Sprint(id)
	return nil
}

func (s *Service) sendZoomVoice(ctx context.Context, j voiceJob, c *VoiceCall, v view, sp *speaker) error {
	z, _, err := s.zoomClient(liveSet(j.sn.set))
	if err != nil {
		if errors.Is(err, errIntegrationOff) {
			return errors.New("the Zoom integration is turned off")
		}
		return err
	}
	wav, err := sp.callWAV(ctx, c.Text, c.Locale)
	if err != nil {
		return err
	}
	text := "🔊 " + v.t("say."+cmp.Or(v.st.Priority, v.a.Severity)) + " · " + v.a.ID + " · " + v.a.Title
	if !j.test {
		for _, l := range v.links() {
			text += "\n" + l.Title + ": " + l.URL
		}
	}
	if err := z.SendChat(ctx, c.Address, text); err != nil {
		return err
	}
	if err := z.SendFile(ctx, c.Address, strings.ToLower(cmp.Or(v.a.ID, "incident"))+".wav", "audio/wav", wav); err != nil {
		return err
	}
	c.State = VoiceSent
	return nil
}

// liveSet is the settings with the integrations taken as they are: a voice delivery that is not a
// dry run uses them live even when the rest of response only plans.
func liveSet(set model.Response) model.Response {
	set.Mode = model.ModeLive
	if set.Graph.Mode == model.ModeDryRun {
		set.Graph.Mode = model.ModeLive
	}
	if set.ZoomAPI.Mode == model.ModeDryRun {
		set.ZoomAPI.Mode = model.ModeLive
	}
	return set
}

// ringTeams places a Teams call; the rest happens as Microsoft reports the call (TeamsEvent).
func (s *Service) ringTeams(ctx context.Context, j voiceJob, c *VoiceCall, v view, sp *speaker) ([]byte, []byte, error) {
	if !strings.HasPrefix(j.sn.public, "https://") {
		return nil, nil, errors.New("Teams calls need the public https address of Umbrella (alerting settings)")
	}
	g, _, err := s.graphClient(liveSet(j.sn.set))
	if err != nil {
		if errors.Is(err, errIntegrationOff) {
			return nil, nil, errors.New("the Microsoft Graph integration is turned off")
		}
		return nil, nil, err
	}
	u, err := g.User(ctx, c.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("no Teams account for %s: %v", c.Address, err)
	}
	audio, err := sp.callWAV(ctx, c.Text, c.Locale)
	if err != nil {
		return nil, nil, err
	}
	var confirm []byte
	if j.sn.set.Voice.AckDigit && !j.test {
		if confirm, err = sp.callWAV(ctx, v.t("say.confirm"), c.Locale); err != nil {
			return nil, nil, err
		}
	}
	// The record must exist before Microsoft reports the call.
	c.State = VoiceDialing
	if err := s.saveCall(ctx, c, audio, confirm); err != nil {
		return nil, nil, err
	}
	id, err := g.CallUser(ctx, j.sn.public+"/api/voice/teams/"+c.token, u.ID, c.Person)
	if err != nil {
		return nil, nil, err
	}
	c.RemoteID = id
	return audio, confirm, nil
}

// teamsNote is a notification Microsoft posts about a call.
type teamsNote struct {
	ChangeType   string `json:"changeType"`
	ResourceURL  string `json:"resourceUrl"`
	Resource     string `json:"resource"`
	ResourceData struct {
		Type          string `json:"@odata.type"`
		State         string `json:"state"`
		Status        string `json:"status"`
		ClientContext string `json:"clientContext"`
		ToneInfo      *struct {
			Tone string `json:"tone"`
		} `json:"toneInfo"`
		ResultInfo *struct {
			Code    int    `json:"code"`
			Subcode int    `json:"subcode"`
			Message string `json:"message"`
		} `json:"resultInfo"`
	} `json:"resourceData"`
}

func loadCall(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, token string) (*VoiceCall, error) {
	var doc []byte
	var tok string
	err := q.QueryRow(ctx, `SELECT doc, token FROM voice_calls WHERE token = $1 FOR UPDATE`, token).Scan(&doc, &tok)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVoiceGone
	}
	if err != nil {
		return nil, err
	}
	c := &VoiceCall{token: tok}
	return c, json.Unmarshal(doc, c)
}

// TeamsEvent handles what Microsoft reports about a Teams call: the answer starts the text, the
// key 1 acknowledges the incident, the end of the text hangs up.
func (s *Service) TeamsEvent(ctx context.Context, token string, body []byte) error {
	var in struct {
		Value []teamsNote `json:"value"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return fmt.Errorf("not a notification: %v", err)
	}
	sn := s.snapshot()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	c, err := loadCall(ctx, tx, token)
	if err != nil {
		return err
	}
	if c.RemoteID == "" || c.State == VoiceFailed {
		return ErrVoiceGone
	}
	g, _, err := s.graphClient(liveSet(sn.set))
	if err != nil {
		return err
	}
	base := sn.public + "/api/voice/audio/" + token
	before := c.State
	var acked bool
	for _, n := range in.Value {
		if !strings.Contains(n.ResourceURL+n.Resource, c.RemoteID) {
			continue
		}
		d := n.ResourceData
		switch {
		case strings.HasSuffix(d.Type, "playPromptOperation"):
			if d.Status == "failed" {
				c.Error = "the text was not played"
				if d.ResultInfo != nil {
					c.Error += ": " + d.ResultInfo.Message
				}
			}
			if (d.Status == "completed" || d.Status == "failed") && !(d.ClientContext == "main" && c.State == VoiceAcked) {
				if err := g.Hangup(ctx, c.RemoteID); err != nil {
					slog.Warn("Teams call not hung up", "call", c.ID, "err", err)
				}
			}
		case n.ChangeType == "deleted" || d.State == "terminated":
			switch c.State {
			case VoiceDialing:
				c.State = VoiceNoAnswer
				if d.ResultInfo != nil && d.ResultInfo.Message != "" {
					c.Error = d.ResultInfo.Message
				}
			case VoiceTalking:
				c.State = VoiceDone
			}
		case d.ToneInfo != nil:
			if d.ToneInfo.Tone == "tone1" && c.State == VoiceTalking && sn.set.Voice.AckDigit && c.AlertID != "" {
				acked = true
				c.State = VoiceAcked
				if err := g.PlayPrompt(ctx, c.RemoteID, "confirm", []string{base + "/confirm.wav"}); err != nil {
					slog.Warn("Teams acknowledgement not played", "call", c.ID, "err", err)
				}
			}
		case d.State == "established" && c.State == VoiceDialing:
			c.State = VoiceTalking
			if sn.set.Voice.AckDigit && c.AlertID != "" {
				if err := g.SubscribeToTone(ctx, c.RemoteID); err != nil {
					slog.Warn("Teams keys not subscribed", "call", c.ID, "err", err)
				}
			}
			uris := make([]string, 0, 3)
			for range max(1, min(sn.set.Voice.Repeat, 3)) {
				uris = append(uris, base+"/main.wav")
			}
			if err := g.PlayPrompt(ctx, c.RemoteID, "main", uris); err != nil {
				c.Error = err.Error()
				_ = g.Hangup(ctx, c.RemoteID)
			}
		}
	}
	if c.State == before && !acked && c.Error == "" {
		return tx.Commit(ctx)
	}
	c.Updated = s.now()
	doc, _ := json.Marshal(c)
	if _, err := tx.Exec(ctx, `UPDATE voice_calls SET doc = $2 WHERE id = $1`, c.ID, doc); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if acked {
		who := cmp.Or(c.Username, c.Person)
		if ak, ok := s.inc.(acker); ok {
			if _, err := ak.Act(ctx, c.AlertID, "ack", who, ""); err != nil && !errors.Is(err, alert.ErrNotOpen) {
				slog.Warn("incident not acknowledged from a call", "alert", c.AlertID, "err", err)
			}
		}
		s.note(ctx, c.AlertID, "response_voice_ack", map[string]string{"via": c.Via, "person": c.Person})
	}
	if c.State != before && c.AlertID != "" && (c.State == VoiceNoAnswer || c.State == VoiceDone) {
		args := map[string]string{"via": c.Via, "person": c.Person, "state": c.State}
		if c.Error != "" {
			args["error"] = c.Error
		}
		s.note(ctx, c.AlertID, "response_voice", args)
	}
	return nil
}

// acker acknowledges incidents (the alert engine).
type acker interface {
	Act(ctx context.Context, id, action, actor, text string) (alert.Alert, error)
}

// Call speaks an incident now by one channel, as asked by a person: to the chosen users, or to
// the people of its route when none is chosen.
func (s *Service) Call(ctx context.Context, id, via string, userIDs []string, actor string) ([]VoiceCall, error) {
	if !model.ValidVoiceVia(via) {
		return nil, ErrVoiceVia
	}
	sn := s.snapshot()
	if sn.set.Voice.Mode == model.ModeOff {
		return nil, ErrVoiceOff
	}
	a, _, err := s.inc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	st, err := loadState(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = &State{AlertID: a.ID, Priority: a.Severity}
	}
	targets := []string{model.TargetRoute}
	if len(userIDs) > 0 {
		targets = nil
	}
	au := sn.audienceOf(a, st.Assessment, targets, userIDs, nil)
	if len(au.people) == 0 {
		return nil, ErrVoiceNobody
	}
	dry := sn.set.Voice.Mode == model.ModeDryRun || sn.set.Mode == model.ModeDryRun
	return s.voice(ctx, voiceJob{sn: sn, a: a, st: st, via: via, by: actor, dry: dry}, au.people), nil
}

// sample is the incident previews and tests speak about.
func (sn snap) sample(locale string) (alert.Alert, *State) {
	opened := time.Now().UTC().Add(-3 * time.Minute)
	a := alert.Alert{ID: "INC-1042", Title: tr(locale, "sample.title"), CIName: "pay-01", Severity: model.SeverityCritical, Status: alert.StatusOpen,
		Signal: "http.errors", OpenedAt: opened, Route: alert.Route{Team: &alert.Ref{ID: "T-0", Name: tr(locale, "sample.team")}}}
	st := &State{AlertID: a.ID, Priority: model.SeverityCritical, Assessment: Assessment{Impact: model.ImpactExtensive,
		Services: []ServiceImpact{{Name: tr(locale, "sample.service"), Direct: true}}}}
	return a, st
}

// PreviewText is the text of a template for a sample incident; an empty template is the saved
// one.
func (s *Service) PreviewText(locale, tmpl, via string) string {
	if !model.ValidLocale(locale) {
		locale = model.LocaleRU
	}
	sn := s.snapshot()
	set := sn.set.Voice
	if strings.TrimSpace(tmpl) != "" {
		set.Templates = map[string]string{locale: tmpl}
	}
	a, st := sn.sample(locale)
	return view{locale: locale, tz: sn.tz, a: a, st: st}.voiceText(set, cmp.Or(via, model.VoiceTeams), 1, true)
}

// Preview is the text of PreviewText spoken, as Teams plays it.
func (s *Service) Preview(ctx context.Context, locale, tmpl, via string) ([]byte, error) {
	if !model.ValidLocale(locale) {
		locale = model.LocaleRU
	}
	text := s.PreviewText(locale, tmpl, via)
	t, err := s.ttsClient(s.snapshot().set.Voice.TTS)
	if err != nil {
		return nil, err
	}
	sp := &speaker{tts: t, cache: map[string][]byte{}}
	return sp.callWAV(ctx, text, locale)
}

// TestCall speaks a test text to one user by one channel, whatever the modes say.
func (s *Service) TestCall(ctx context.Context, via string, u model.User) (VoiceCall, error) {
	if !model.ValidVoiceVia(via) {
		return VoiceCall{}, ErrVoiceVia
	}
	sn := s.snapshot()
	l := sn.listenerOf(userPerson(u))
	a, st := sn.sample(l.locale)
	a.ID, a.Title = "TEST", tr(l.locale, "say.test.title")
	text := map[string]string{model.LocaleRU: tr(model.LocaleRU, "say.test"), model.LocaleEN: tr(model.LocaleEN, "say.test")}
	calls := s.voice(ctx, voiceJob{sn: sn, a: a, st: st, via: via, test: true, text: text}, []person{userPerson(u)})
	c := calls[0]
	if c.State == VoiceFailed {
		return c, errors.New(c.Error)
	}
	return c, nil
}

// voiceStep speaks the incident at an escalation step by the voice methods of the step.
func (p *pass) voiceStep(methods []string, au audience, level int, run *StepRun) {
	for _, m := range methods {
		via := model.VoiceVia(m)
		if via == "" {
			continue
		}
		if p.sn.set.Voice.Mode == model.ModeOff {
			run.Failed = append(run.Failed, m+" ("+ErrVoiceOff.Error()+")")
			continue
		}
		if len(au.people) == 0 {
			continue
		}
		dry := p.dry || p.sn.set.Voice.Mode == model.ModeDryRun
		for _, c := range p.s.voice(p.ctx, voiceJob{sn: p.sn, a: p.a, st: p.st, via: via, level: level, dry: dry}, au.people) {
			if c.State == VoiceFailed {
				run.Failed = append(run.Failed, m+": "+c.Person+" ("+c.Error+")")
			} else {
				run.Reached = append(run.Reached, m+": "+c.Person)
			}
		}
	}
}
