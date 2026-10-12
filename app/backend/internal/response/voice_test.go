package response_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/response"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// fakeTTS plays Piper (POST JSON) and rhvoice-rest (GET /say): WAV of 22050 Hz, or OGG for
// format=opus.
type fakeTTS struct {
	srv  *httptest.Server
	mu   sync.Mutex
	asks []map[string]string
}

func newFakeTTS(t *testing.T) *fakeTTS {
	f := &fakeTTS{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ask := map[string]string{"path": r.URL.Path}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&ask)
			ask["path"] = r.URL.Path
		} else {
			for k := range r.URL.Query() {
				ask[k] = r.URL.Query().Get(k)
			}
		}
		f.mu.Lock()
		f.asks = append(f.asks, ask)
		f.mu.Unlock()
		if ask["format"] == "opus" {
			w.Header().Set("Content-Type", "audio/ogg")
			_, _ = w.Write(append([]byte("OggS"), make([]byte, 200)...))
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav22k(11025))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTTS) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.asks {
		out = append(out, a["text"]+" |"+a["voice"])
	}
	return out
}

func wav22k(n int) []byte {
	b := make([]byte, 44+n*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 22050)
	binary.LittleEndian.PutUint32(b[28:], 44100)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n*2))
	for i := range n {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(int16(i%200*100)))
	}
	return b
}

// voiceSetup turns voice calls on with the fake speech server; the lead speaks Russian, the
// engineer English, and P1 calls the route by method.
func voiceSetup(t *testing.T, mode, method string) (*env, *fakeTTS) {
	v := setup(t, model.ModeLive)
	tts := newFakeTTS(t)
	v.st.Write(func(d *store.Data) {
		d.Users["U-1"].Locale = "ru"
		d.Users["U-2"].Locale = "en"
		r := &d.Settings.Response
		r.Voice = model.DefaultVoice()
		r.Voice.Mode = mode
		r.Voice.TTS.URL = tts.srv.URL
		for i := range r.Policies {
			if r.Policies[i].Priority == model.SeverityCritical {
				r.Policies[i].Steps = []model.EscalationStep{{AfterMinutes: 0, Targets: []string{model.TargetRoute}, Methods: []string{method}}}
				r.Policies[i].WarRoom.Enabled, r.Policies[i].Bridge, r.Policies[i].Jira = false, "", model.JiraPolicy{PostmortemDays: 5}
			}
		}
	})
	return v, tts
}

func teamsNote(t *testing.T, v *env, token string, body string) error {
	t.Helper()
	return v.r.TeamsEvent(context.Background(), token, []byte(body))
}

func callNote(id, extra string) string {
	return fmt.Sprintf(`{"value":[{"changeType":"updated","resourceUrl":"/communications/calls/%s","resourceData":{"@odata.type":"#microsoft.graph.call",%s}}]}`, id, extra)
}

func TestVoiceTeamsCall(t *testing.T) {
	ctx := context.Background()
	v, tts := voiceSetup(t, model.ModeLive, model.CommVoiceTeams)
	a := v.fire(t, "pay-01", model.SeverityError, model.MethodRED, alert.SourceFiring)
	v.tick(t)

	// Teams rings the lead and the engineer, each with the text in their own language.
	rings := v.api.take(path("/communications/calls"))
	if len(rings) != 2 {
		t.Fatalf("calls = %+v", rings)
	}
	calls, err := v.r.Calls(ctx, a.ID)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v %v", calls, err)
	}
	byLocale := map[string]response.VoiceCall{}
	for _, c := range calls {
		byLocale[c.Locale] = c
		if c.State != response.VoiceDialing || c.Via != model.VoiceTeams || c.Level != 1 {
			t.Fatalf("call = %+v", c)
		}
	}
	ru, en := byLocale["ru"], byLocale["en"]
	if !strings.Contains(ru.Text, "Приоритет первый, критический") || !strings.Contains(ru.Text, "нажмите 1") || !strings.Contains(ru.Text, "номер "+strings.TrimPrefix(a.ID, "INC-")) {
		t.Fatalf("ru text = %q", ru.Text)
	}
	if !strings.Contains(en.Text, "Priority one, critical") || !strings.Contains(en.Text, "press 1") {
		t.Fatalf("en text = %q", en.Text)
	}
	got := strings.Join(tts.texts(), "\n")
	if !strings.Contains(got, "|ru_RU-irina-medium") || !strings.Contains(got, "|en_US-lessac-medium") || !strings.Contains(got, "Инцидент подтверждён") {
		t.Fatalf("speech asks = %s", got)
	}
	if c := codes(t, v.e, a.ID); count(c, "response_voice") != 2 {
		t.Fatalf("codes = %v", c)
	}

	// The call of the lead: Microsoft calls back at the address with the token of the call.
	var ring call
	for _, r := range rings {
		target := r.Body["targets"].([]any)[0].(map[string]any)["identity"].(map[string]any)["user"].(map[string]any)
		if target["id"] == "id-lead@corp.example" {
			ring = r
		}
	}
	if ring.Body == nil || ring.Auth != "Bearer graph-access" || ring.Body["mediaConfig"].(map[string]any)["@odata.type"] != "#microsoft.graph.serviceHostedMediaConfig" {
		t.Fatalf("ring = %+v", ring)
	}
	cb := ring.Body["callbackUri"].(string)
	if !strings.HasPrefix(cb, "https://umbrella.example/api/voice/teams/") {
		t.Fatalf("callback = %s", cb)
	}
	token := strings.TrimPrefix(cb, "https://umbrella.example/api/voice/teams/")
	remote := ru.RemoteID

	// Answered: keys are listened to and the text is played twice.
	if err := teamsNote(t, v, token, callNote(remote, `"state":"established"`)); err != nil {
		t.Fatal(err)
	}
	if n := v.api.take(path("/communications/calls/" + remote + "/subscribeToTone")); len(n) != 1 {
		t.Fatalf("tone subscriptions = %+v", n)
	}
	plays := v.api.take(path("/communications/calls/" + remote + "/playPrompt"))
	if len(plays) != 1 || len(plays[0].Body["prompts"].([]any)) != 2 {
		t.Fatalf("plays = %+v", plays)
	}
	uri := plays[0].Body["prompts"].([]any)[0].(map[string]any)["mediaInfo"].(map[string]any)["uri"].(string)
	if uri != "https://umbrella.example/api/voice/audio/"+token+"/main.wav" {
		t.Fatalf("uri = %s", uri)
	}
	audio, err := v.r.Audio(ctx, token, "main")
	if err != nil || string(audio[:4]) != "RIFF" || binary.LittleEndian.Uint32(audio[24:]) != 16000 {
		t.Fatalf("audio is 16 kHz WAV: %v", err)
	}
	if _, err := v.r.Audio(ctx, strings.Repeat("0", 48), "main"); !errors.Is(err, response.ErrVoiceGone) {
		t.Fatalf("unknown token: %v", err)
	}

	// The lead presses 1: the incident is acknowledged in their name and the confirmation plays.
	if err := teamsNote(t, v, token, callNote(remote, `"state":"established","toneInfo":{"tone":"tone1","sequenceId":1}`)); err != nil {
		t.Fatal(err)
	}
	got2, _, _ := v.e.Get(ctx, a.ID)
	if got2.Status != alert.StatusAcknowledged || got2.AckedBy != "lead" {
		t.Fatalf("incident = %s by %s", got2.Status, got2.AckedBy)
	}
	if plays = v.api.take(path("/communications/calls/" + remote + "/playPrompt")); len(plays) != 2 || plays[1].Body["clientContext"] != "confirm" {
		t.Fatalf("plays = %+v", plays)
	}
	// The text ends (no hang-up: the confirmation is still to play), then the confirmation ends.
	op := `{"value":[{"changeType":"updated","resourceUrl":"/communications/calls/%s/operations/op","resourceData":{"@odata.type":"#microsoft.graph.playPromptOperation","status":"completed","clientContext":"%s"}}]}`
	if err := teamsNote(t, v, token, fmt.Sprintf(op, remote, "main")); err != nil {
		t.Fatal(err)
	}
	hangups := func() int {
		n := 0
		for _, c := range v.api.take(func(c call) bool { return c.Method == http.MethodDelete }) {
			if c.Path == "/communications/calls/"+remote {
				n++
			}
		}
		return n
	}
	if hangups() != 0 {
		t.Fatal("hung up before the confirmation")
	}
	if err := teamsNote(t, v, token, fmt.Sprintf(op, remote, "confirm")); err != nil {
		t.Fatal(err)
	}
	if hangups() != 1 {
		t.Fatal("not hung up after the confirmation")
	}
	if err := teamsNote(t, v, token, `{"value":[{"changeType":"deleted","resourceUrl":"/communications/calls/`+remote+`","resourceData":{"@odata.type":"#microsoft.graph.call","state":"terminated"}}]}`); err != nil {
		t.Fatal(err)
	}
	calls, _ = v.r.Calls(ctx, a.ID)
	for _, c := range calls {
		if c.Locale == "ru" && c.State != response.VoiceAcked {
			t.Fatalf("lead call = %+v", c)
		}
	}
	if c := codes(t, v.e, a.ID); count(c, "response_voice_ack") != 1 {
		t.Fatalf("codes = %v", c)
	}

	// The engineer never answers.
	var enToken string
	for _, r := range rings {
		if cb := r.Body["callbackUri"].(string); !strings.HasSuffix(cb, token) {
			enToken = cb[strings.LastIndex(cb, "/")+1:]
		}
	}
	if err := teamsNote(t, v, enToken, `{"value":[{"changeType":"deleted","resourceUrl":"/communications/calls/`+en.RemoteID+`","resourceData":{"@odata.type":"#microsoft.graph.call","state":"terminated","resultInfo":{"code":408,"message":"No answer"}}}]}`); err != nil {
		t.Fatal(err)
	}
	calls, _ = v.r.Calls(ctx, a.ID)
	for _, c := range calls {
		if c.Locale == "en" && (c.State != response.VoiceNoAnswer || c.Error != "No answer") {
			t.Fatalf("engineer call = %+v", c)
		}
	}
	// A notification for another call is not taken.
	if err := teamsNote(t, v, "nope", callNote(remote, `"state":"established"`)); !errors.Is(err, response.ErrVoiceGone) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestVoiceTelegramAndZoom(t *testing.T) {
	ctx := context.Background()
	defer response.SetFFmpeg(func() (string, error) { return "", errors.New("none") })()
	v, _ := voiceSetup(t, model.ModeLive, model.CommEmail)
	v.st.Write(func(d *store.Data) { d.Settings.Alerting.Notify.Telegram.Bot = true })
	a := v.fire(t, "pay-01", model.SeverityError, model.MethodRED, alert.SourceFiring)

	// Telegram bots cannot call: each person of the route gets the audio, here as a WAV file
	// (Piper gives WAV and there is no ffmpeg), with the acknowledgement button of the bot.
	calls, err := v.r.Call(ctx, a.ID, model.VoiceTelegram, nil, "admin")
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v %v", calls, err)
	}
	voices := v.tg.Voices()
	if len(voices) != 2 || voices[0].Method != "document" || voices[0].FileName != "incident.wav" || !strings.Contains(string(voices[0].Keyboard), "ack:"+a.ID) {
		t.Fatalf("voices = %+v", voices)
	}
	for _, c := range calls {
		if c.State != response.VoiceSent || c.By != "admin" || c.Voice {
			t.Fatalf("call = %+v", c)
		}
	}

	// An engine that gives Opus makes it a voice message.
	v.st.Write(func(d *store.Data) { d.Settings.Response.Voice.TTS.Provider = model.TTSRHVoice })
	if _, err := v.r.Call(ctx, a.ID, model.VoiceTelegram, []string{"U-1"}, "admin"); err != nil {
		t.Fatal(err)
	}
	voices = v.tg.Voices()
	if last := voices[len(voices)-1]; last.Method != "voice" || last.ChatID != "1001" || string(last.Audio[:4]) != "OggS" {
		t.Fatalf("voice = %+v", last)
	}

	// Zoom: a chat message with the links and the audio file, to the e-mail of the person.
	if _, err := v.r.Call(ctx, a.ID, model.VoiceZoom, []string{"U-2"}, "admin"); err != nil {
		t.Fatal(err)
	}
	msgs := v.api.take(path("/chat/users/me/messages"))
	files := v.api.take(path("/chat/users/me/messages/files"))
	if len(msgs) != 1 || msgs[0].Body["to_contact"] != "eng@corp.example" || !strings.Contains(fmt.Sprint(msgs[0].Body["message"]), a.ID) {
		t.Fatalf("zoom messages = %+v", msgs)
	}
	if len(files) != 1 || !strings.Contains(fmt.Sprint(files[0].Body), "eng@corp.example") || !strings.Contains(fmt.Sprint(files[0].Body), "RIFF") {
		t.Fatalf("zoom files = %+v", files)
	}

	// Nobody to reach and an unknown channel.
	if _, err := v.r.Call(ctx, a.ID, "fax", nil, "admin"); !errors.Is(err, response.ErrVoiceVia) {
		t.Fatalf("fax: %v", err)
	}
	v.st.Write(func(d *store.Data) { d.Settings.Response.Voice.Mode = model.ModeOff })
	if _, err := v.r.Call(ctx, a.ID, model.VoiceZoom, nil, "admin"); !errors.Is(err, response.ErrVoiceOff) {
		t.Fatalf("off: %v", err)
	}
}

func TestVoiceDryRunAndTest(t *testing.T) {
	ctx := context.Background()
	v, tts := voiceSetup(t, model.ModeDryRun, model.CommVoiceTelegram)
	a := v.fire(t, "pay-01", model.SeverityError, model.MethodRED, alert.SourceFiring)
	v.tick(t)
	calls, _ := v.r.Calls(ctx, a.ID)
	if len(calls) != 2 || calls[0].State != response.VoicePlanned || !calls[0].DryRun {
		t.Fatalf("calls = %+v", calls)
	}
	if len(tts.texts()) != 0 || len(v.tg.Voices()) != 0 {
		t.Fatal("a dry run sends nothing")
	}
	st := v.state(t, a.ID)
	if len(st.Steps) != 1 || len(st.Steps[0].Reached) != 2 {
		t.Fatalf("steps = %+v", st.Steps)
	}

	// A test call speaks the test text in the language of the user, whatever the mode.
	var u model.User
	v.st.Read(func(d *store.Data) { u = *d.Users["U-1"] })
	c, err := v.r.TestCall(ctx, model.VoiceTelegram, u)
	if err != nil || c.State != response.VoiceSent || !strings.HasPrefix(c.Text, "Это проверка") || c.AlertID != "" {
		t.Fatalf("test = %+v %v", c, err)
	}
	// The preview text of a template.
	if got := v.r.PreviewText("en", "Incident {number}: {title}, {services}", ""); got != "Incident 1042: HTTP 5xx errors growing on pay-01, Payments" {
		t.Fatalf("preview = %q", got)
	}
	wav, err := v.r.Preview(ctx, "ru", "", "")
	if err != nil || string(wav[:4]) != "RIFF" {
		t.Fatalf("preview audio: %v", err)
	}
}
