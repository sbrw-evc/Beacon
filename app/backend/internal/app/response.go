package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/response"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const responseSecretPath = "response"

var errResponseUnavailable = errors.New("incident response needs PostgreSQL")

func init() {
	statuses = append(statuses,
		errStatus{response.ErrOff, http.StatusConflict, "response_off"},
		errStatus{response.ErrBadAction, http.StatusBadRequest, "bad_action"},
		errStatus{errResponseUnavailable, http.StatusServiceUnavailable, "response_unavailable"},
		errStatus{response.ErrVoiceOff, http.StatusConflict, "voice_off"},
		errStatus{response.ErrVoiceVia, http.StatusBadRequest, "voice_via_invalid"},
		errStatus{response.ErrVoiceNobody, http.StatusConflict, "voice_nobody"},
		errStatus{response.ErrVoiceGone, http.StatusNotFound, "voice_gone"},
	)
}

// ResponseService keeps the settings of incident response; the Jira API token, the Microsoft
// Graph client secret and refresh token and the Zoom client secret are kept in OpenBao.
type ResponseService struct {
	st      *store.Store
	secrets Secrets
	n       *notify.Service
}

func NewResponseService(st *store.Store, secrets Secrets, n *notify.Service) *ResponseService {
	return &ResponseService{st: st, secrets: secrets, n: n}
}

type JiraView struct {
	model.JiraSettings
	HasToken bool `json:"has_token"`
}

type GraphView struct {
	model.GraphSettings
	HasSecret  bool `json:"has_secret"`
	HasRefresh bool `json:"has_refresh"`
}

type ZoomAPIView struct {
	model.ZoomAPISettings
	HasSecret bool `json:"has_secret"`
}

type VoiceView struct {
	model.VoiceSettings
	HasKey bool `json:"has_key"`
	// FFmpeg: ffmpeg is installed, so Piper audio goes to Telegram as a voice message.
	FFmpeg           bool              `json:"ffmpeg"`
	DefaultTemplates map[string]string `json:"default_templates"`
	Placeholders     []string          `json:"placeholders"`
}

type ResponseView struct {
	Mode        string                 `json:"mode"`
	ActiveSince *time.Time             `json:"active_since,omitempty"`
	Impact      model.ImpactPolicy     `json:"impact"`
	Policies    []model.ResponsePolicy `json:"policies"`
	Jira        JiraView               `json:"jira"`
	Graph       GraphView              `json:"graph"`
	Zoom        ZoomAPIView            `json:"zoom"`
	Voice       VoiceView              `json:"voice"`
	// Channels: the backup notification channels that are ready (email, telegram, teams, zoom)
	// and PagerDuty:
	// escalation messages go through them.
	Channels        map[string]bool        `json:"channels"`
	DefaultImpact   model.ImpactPolicy     `json:"default_impact"`
	DefaultPolicies []model.ResponsePolicy `json:"default_policies"`
	UpdatedAt       *time.Time             `json:"updated_at,omitempty"`
	UpdatedBy       string                 `json:"updated_by,omitempty"`
}

func (s *ResponseService) View() ResponseView {
	var r model.Response
	var pdOn bool
	s.st.Read(func(d *store.Data) { r, pdOn = d.Settings.Response.Effective(), d.Settings.Alerting.PagerDuty.Enabled })
	ch := map[string]bool{model.CommPagerDuty: pdOn}
	for _, c := range notify.Channels() {
		ch[c] = s.n.Ready(c)
	}
	if r.Jira.Labels == nil {
		r.Jira.Labels = []string{}
	}
	return ResponseView{Mode: r.Mode, ActiveSince: r.ActiveSince, Impact: r.Impact, Policies: r.Policies,
		Jira:  JiraView{JiraSettings: r.Jira, HasToken: r.Jira.TokenRef != ""},
		Graph: GraphView{GraphSettings: r.Graph, HasSecret: r.Graph.ClientSecretRef != "", HasRefresh: r.Graph.RefreshTokenRef != ""},
		Zoom:  ZoomAPIView{ZoomAPISettings: r.ZoomAPI, HasSecret: r.ZoomAPI.ClientSecretRef != ""},
		Voice: VoiceView{VoiceSettings: r.Voice, HasKey: r.Voice.TTS.APIKeyRef != "", FFmpeg: ffmpegInstalled(),
			DefaultTemplates: model.DefaultVoiceTemplates, Placeholders: model.VoicePlaceholders},
		Channels: ch, DefaultImpact: model.DefaultImpactPolicy(), DefaultPolicies: model.DefaultResponsePolicies(), UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy}
}

type ResponsePolicyInput struct {
	Mode     string                 `json:"mode"`
	Impact   model.ImpactPolicy     `json:"impact"`
	Policies []model.ResponsePolicy `json:"policies"`
}

// SavePolicy saves the mode, the impact policy and the priority policies. People and teams that
// do not exist are dropped from the steps.
func (s *ResponseService) SavePolicy(actor string, in ResponsePolicyInput) (ResponseView, error) {
	if in.Mode == "" {
		in.Mode = model.ModeOff
	}
	if !model.ValidMode(in.Mode) {
		return ResponseView{}, invalid("mode_invalid", nil)
	}
	if err := in.Impact.Validate(); err != nil {
		return ResponseView{}, invalid("impact_invalid", err)
	}
	pols, err := model.NormalizePolicies(in.Policies)
	if err != nil {
		return ResponseView{}, invalid("policy_invalid", err)
	}
	now := time.Now().UTC()
	s.st.Write(func(d *store.Data) {
		known := func(ids []string, ok func(string) bool) []string {
			out := []string{}
			for _, id := range ids {
				if ok(id) {
					out = append(out, id)
				}
			}
			return out
		}
		user := func(id string) bool { return d.Users[id] != nil }
		team := func(id string) bool { return d.Teams[id] != nil }
		for i := range pols {
			for j := range pols[i].Steps {
				st := &pols[i].Steps[j]
				st.UserIDs, st.TeamIDs = known(st.UserIDs, user), known(st.TeamIDs, team)
			}
			pols[i].WarRoom.UserIDs = known(pols[i].WarRoom.UserIDs, user)
		}
		r := &d.Settings.Response
		if r.Mode == "" {
			r.Mode = model.ModeOff
		}
		if r.Mode == model.ModeOff && in.Mode != model.ModeOff {
			r.ActiveSince = &now
		}
		r.Mode, r.Impact, r.Policies = in.Mode, in.Impact, pols
		r.UpdatedAt, r.UpdatedBy = &now, actor
		enabled := []string{}
		for _, p := range pols {
			if p.Enabled {
				enabled = append(enabled, model.SeverityPriority(p.Priority))
			}
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.response", Detail: fmt.Sprintf("mode=%s policies=%s", in.Mode, strings.Join(enabled, ","))})
	})
	return s.View(), nil
}

func modeOf(v string) (string, error) {
	if v == "" {
		return model.ModeOff, nil
	}
	if !model.ValidMode(v) {
		return "", invalid("mode_invalid", nil)
	}
	return v, nil
}

type JiraInput struct {
	Mode           string            `json:"mode"`
	BaseURL        string            `json:"base_url"`
	Email          string            `json:"email"`
	Token          string            `json:"token"`
	Project        string            `json:"project"`
	TaskType       string            `json:"task_type"`
	PostmortemType string            `json:"postmortem_type"`
	Priorities     map[string]string `json:"priorities"`
	Labels         []string          `json:"labels"`
	LinkType       string            `json:"link_type"`
	DoneTransition string            `json:"done_transition"`
}

var (
	jiraProject = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)
	jiraLabel   = regexp.MustCompile(`^[^\s]{1,100}$`)
)

func (s *ResponseService) SaveJira(ctx context.Context, actor string, in JiraInput) (ResponseView, error) {
	mode, err := modeOf(in.Mode)
	if err != nil {
		return ResponseView{}, err
	}
	var cur model.JiraSettings
	s.st.Read(func(d *store.Data) { cur = d.Settings.Response.Jira })
	base, err := model.NormalizeHTTPS(in.BaseURL)
	if err != nil {
		return ResponseView{}, invalid("url_invalid", err)
	}
	email := strings.TrimSpace(in.Email)
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return ResponseView{}, invalid("email_invalid", nil)
		}
	}
	project := strings.ToUpper(strings.TrimSpace(in.Project))
	if project != "" && !jiraProject.MatchString(project) {
		return ResponseView{}, invalid("project_invalid", nil)
	}
	labels := []string{}
	for _, l := range in.Labels {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !jiraLabel.MatchString(l) {
			return ResponseView{}, invalid("label_invalid", fmt.Errorf("%q", l))
		}
		labels = append(labels, l)
	}
	prios := map[string]string{}
	for k, v := range in.Priorities {
		if !model.ValidSeverity(k) {
			return ResponseView{}, invalid("severity_invalid", nil)
		}
		if v = strings.TrimSpace(v); v != "" {
			prios[k] = v
		}
	}
	ref := cur.TokenRef
	if t := strings.TrimSpace(in.Token); t != "" {
		if ref, err = putSecret(ctx, s.secrets, responseSecretPath, "jira_token", t); err != nil {
			return ResponseView{}, err
		}
	}
	if mode == model.ModeLive && (base == "" || email == "" || project == "" || ref == "") {
		return ResponseView{}, invalid("jira_incomplete", nil)
	}
	def := model.DefaultJira()
	j := model.JiraSettings{Mode: mode, BaseURL: base, Email: email, TokenRef: ref, Project: project,
		TaskType: cmp.Or(strings.TrimSpace(in.TaskType), def.TaskType), PostmortemType: cmp.Or(strings.TrimSpace(in.PostmortemType), def.PostmortemType),
		Priorities: prios, Labels: labels, LinkType: strings.TrimSpace(in.LinkType), DoneTransition: strings.TrimSpace(in.DoneTransition)}
	s.st.Write(func(d *store.Data) {
		d.Settings.Response.Jira = j
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.response.jira", Detail: fmt.Sprintf("mode=%s site=%s project=%s", mode, base, project)})
	})
	return s.View(), nil
}

type GraphInput struct {
	Mode         string `json:"mode"`
	TenantID     string `json:"tenant_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	Account      string `json:"account"`
	LoginURL     string `json:"login_url"`
	GraphURL     string `json:"graph_url"`
}

var guidOrDomain = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,253}$`)

func (s *ResponseService) SaveGraph(ctx context.Context, actor string, in GraphInput) (ResponseView, error) {
	mode, err := modeOf(in.Mode)
	if err != nil {
		return ResponseView{}, err
	}
	var cur model.GraphSettings
	s.st.Read(func(d *store.Data) { cur = d.Settings.Response.Graph })
	tenant, client := strings.TrimSpace(in.TenantID), strings.TrimSpace(in.ClientID)
	if (tenant != "" && !guidOrDomain.MatchString(tenant)) || (client != "" && !guidOrDomain.MatchString(client)) {
		return ResponseView{}, invalid("graph_id_invalid", nil)
	}
	login, err := model.NormalizeHTTPS(in.LoginURL)
	if err != nil {
		return ResponseView{}, invalid("url_invalid", err)
	}
	api, err := model.NormalizeHTTPS(in.GraphURL)
	if err != nil {
		return ResponseView{}, invalid("url_invalid", err)
	}
	secretRef, refreshRef := cur.ClientSecretRef, cur.RefreshTokenRef
	if v := strings.TrimSpace(in.ClientSecret); v != "" {
		if secretRef, err = putSecret(ctx, s.secrets, responseSecretPath, "graph_client_secret", v); err != nil {
			return ResponseView{}, err
		}
	}
	if v := strings.TrimSpace(in.RefreshToken); v != "" {
		if refreshRef, err = putSecret(ctx, s.secrets, responseSecretPath, "graph_refresh_token", v); err != nil {
			return ResponseView{}, err
		}
	}
	if mode == model.ModeLive && (tenant == "" || client == "" || secretRef == "" || refreshRef == "") {
		return ResponseView{}, invalid("graph_incomplete", nil)
	}
	g := model.GraphSettings{Mode: mode, TenantID: tenant, ClientID: client, ClientSecretRef: secretRef, RefreshTokenRef: refreshRef,
		Account: strings.TrimSpace(in.Account), LoginURL: login, GraphURL: api}
	s.st.Write(func(d *store.Data) {
		d.Settings.Response.Graph = g
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.response.graph", Detail: fmt.Sprintf("mode=%s tenant=%s", mode, tenant)})
	})
	return s.View(), nil
}

type ZoomAPIInput struct {
	Mode         string `json:"mode"`
	AccountID    string `json:"account_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	User         string `json:"user"`
	OAuthURL     string `json:"oauth_url"`
	APIURL       string `json:"api_url"`
}

func (s *ResponseService) SaveZoom(ctx context.Context, actor string, in ZoomAPIInput) (ResponseView, error) {
	mode, err := modeOf(in.Mode)
	if err != nil {
		return ResponseView{}, err
	}
	var cur model.ZoomAPISettings
	s.st.Read(func(d *store.Data) { cur = d.Settings.Response.ZoomAPI })
	account, client, user := strings.TrimSpace(in.AccountID), strings.TrimSpace(in.ClientID), strings.TrimSpace(in.User)
	if (account != "" && !guidOrDomain.MatchString(account)) || (client != "" && !guidOrDomain.MatchString(client)) || strings.ContainsAny(user, "/ ?#") {
		return ResponseView{}, invalid("zoom_id_invalid", nil)
	}
	oauth, err := model.NormalizeHTTPS(in.OAuthURL)
	if err != nil {
		return ResponseView{}, invalid("url_invalid", err)
	}
	api, err := model.NormalizeHTTPS(in.APIURL)
	if err != nil {
		return ResponseView{}, invalid("url_invalid", err)
	}
	ref := cur.ClientSecretRef
	if v := strings.TrimSpace(in.ClientSecret); v != "" {
		if ref, err = putSecret(ctx, s.secrets, responseSecretPath, "zoom_client_secret", v); err != nil {
			return ResponseView{}, err
		}
	}
	if mode == model.ModeLive && (account == "" || client == "" || ref == "") {
		return ResponseView{}, invalid("zoom_incomplete", nil)
	}
	z := model.ZoomAPISettings{Mode: mode, AccountID: account, ClientID: client, ClientSecretRef: ref, User: cmp.Or(user, "me"), OAuthURL: oauth, APIURL: api}
	s.st.Write(func(d *store.Data) {
		d.Settings.Response.ZoomAPI = z
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.response.zoom", Detail: fmt.Sprintf("mode=%s account=%s", mode, account)})
	})
	return s.View(), nil
}

func ffmpegInstalled() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

type VoiceInput struct {
	Mode      string            `json:"mode"`
	Provider  string            `json:"provider"`
	URL       string            `json:"url"`
	Voices    map[string]string `json:"voices"`
	Model     string            `json:"model"`
	APIKey    string            `json:"api_key"`
	ClearKey  bool              `json:"clear_key"`
	Templates map[string]string `json:"templates"`
	Repeat    int               `json:"repeat"`
	AckDigit  bool              `json:"ack_digit"`
}

var voiceName = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,100}$`)

// SaveVoice saves the voice settings: the speech server (its key in OpenBao), the voices and the
// templates of each language.
func (s *ResponseService) SaveVoice(ctx context.Context, actor string, in VoiceInput) (ResponseView, error) {
	mode, err := modeOf(in.Mode)
	if err != nil {
		return ResponseView{}, err
	}
	if !model.ValidTTS(in.Provider) {
		return ResponseView{}, invalid("tts_invalid", nil)
	}
	addr, err := normalizeServer(in.URL)
	if err != nil {
		return ResponseView{}, invalid("url_invalid", err)
	}
	voices := map[string]string{}
	for loc, v := range in.Voices {
		if !model.ValidLocale(loc) {
			return ResponseView{}, invalid("locale_invalid", nil)
		}
		if v = strings.TrimSpace(v); v != "" {
			if !voiceName.MatchString(v) {
				return ResponseView{}, invalid("voice_invalid", fmt.Errorf("%q", v))
			}
			voices[loc] = v
		}
	}
	tmpls := map[string]string{}
	for loc, t := range in.Templates {
		if !model.ValidLocale(loc) {
			return ResponseView{}, invalid("locale_invalid", nil)
		}
		t = strings.TrimSpace(t)
		if len([]rune(t)) > model.MaxVoiceTemplate {
			return ResponseView{}, invalid("template_too_long", nil)
		}
		if t != "" && t != model.DefaultVoiceTemplates[loc] {
			tmpls[loc] = t
		}
	}
	if in.Repeat < 1 || in.Repeat > 3 {
		return ResponseView{}, invalid("repeat_invalid", nil)
	}
	var cur model.TTSSettings
	s.st.Read(func(d *store.Data) { cur = d.Settings.Response.Voice.TTS })
	ref := cur.APIKeyRef
	if in.ClearKey {
		ref = ""
	}
	if v := strings.TrimSpace(in.APIKey); v != "" {
		if ref, err = putSecret(ctx, s.secrets, responseSecretPath, "tts_api_key", v); err != nil {
			return ResponseView{}, err
		}
	}
	if mode != model.ModeOff && addr == "" {
		return ResponseView{}, invalid("voice_incomplete", nil)
	}
	v := model.VoiceSettings{Mode: mode, Repeat: in.Repeat, AckDigit: in.AckDigit, Templates: tmpls,
		TTS: model.TTSSettings{Provider: in.Provider, URL: addr, Voices: voices, Model: strings.TrimSpace(in.Model), APIKeyRef: ref}}
	s.st.Write(func(d *store.Data) {
		d.Settings.Response.Voice = v
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.response.voice", Detail: fmt.Sprintf("mode=%s engine=%s", mode, in.Provider)})
	})
	return s.View(), nil
}

// normalizeServer checks the address of a self-hosted server: http or https, no query.
func normalizeServer(v string) (string, error) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("the address must be an http or https URL without a query")
	}
	return v, nil
}

type SimulateInput struct {
	Severity  string `json:"severity"`
	ServiceID string `json:"service_id"`
	Method    string `json:"method"`
	// Incidents: how many active incidents the service would have, this one included.
	Incidents int `json:"incidents"`
}

type SimStep struct {
	AfterMinutes int      `json:"after_minutes"`
	Methods      []string `json:"methods"`
	People       []string `json:"people"`
}

type SimulateView struct {
	Assessment response.Assessment   `json:"assessment"`
	Policy     *model.ResponsePolicy `json:"policy"`
	Steps      []SimStep             `json:"steps"`
	Room       []string              `json:"room"`
	Team       *alert.Ref            `json:"team,omitempty"`
}

// Simulate shows what response would do for an incident of a service: the assessment, the
// policy and who each step would reach (the owning team of the service stands for the route).
func (s *ResponseService) Simulate(in SimulateInput) (SimulateView, error) {
	if !model.ValidSeverity(in.Severity) {
		return SimulateView{}, invalid("severity_invalid", nil)
	}
	var set model.Response
	var cat response.Catalog
	cat.Services, cat.Teams, cat.Users = map[string]model.Service{}, map[string]model.Team{}, map[string]model.User{}
	s.st.Read(func(d *store.Data) {
		set = d.Settings.Response.Effective()
		for id, v := range d.Services {
			cat.Services[id] = *v
		}
		for id, v := range d.Teams {
			cat.Teams[id] = *v
		}
		for id, v := range d.Users {
			u := *v
			u.Avatar = nil
			cat.Users[id] = u
		}
	})
	a := alert.Alert{ID: "INC-0", Title: "simulation", Severity: in.Severity, Method: in.Method, Status: alert.StatusOpen,
		Route: alert.Route{Services: []alert.Ref{}, People: []alert.Person{}, Owners: []alert.Person{}}}
	counts := map[string]int{}
	if in.ServiceID != "" {
		svc, ok := cat.Services[in.ServiceID]
		if !ok {
			return SimulateView{}, ErrNotFound
		}
		a.Route.Services = []alert.Ref{{ID: svc.ID, Name: svc.Name}}
		a.Route.Service = &a.Route.Services[0]
		counts[svc.ID] = max(1, in.Incidents)
		if t, ok := cat.Teams[svc.OwnerTeamID]; ok {
			a.Route.Team = &alert.Ref{ID: t.ID, Name: t.Name}
			for _, u := range cat.Users {
				if !u.Disabled && u.InTeam(t.ID) {
					role := "member"
					if u.ID == t.LeadID {
						role = "lead"
					}
					a.Route.People = append(a.Route.People, alert.Person{UserID: u.ID, Name: cmp.Or(u.Name, u.Username), Email: u.Email, Telegram: u.Telegram, Role: role})
				}
			}
			if ch := alert.TeamChannel(t); !ch.Empty() {
				a.Route.Channel = &ch
			}
		}
	}
	as := response.Assess(set.Impact, cat, a, counts, time.Now().UTC())
	out := SimulateView{Assessment: as, Steps: []SimStep{}, Room: []string{}, Team: a.Route.Team}
	pol, ok := set.PolicyFor(as.Priority)
	if !ok {
		return out, nil
	}
	out.Policy = &pol
	for _, st := range pol.Steps {
		out.Steps = append(out.Steps, SimStep{AfterMinutes: st.AfterMinutes, Methods: st.Methods, People: response.People(cat, a, as, st.Targets, st.UserIDs, st.TeamIDs)})
	}
	if pol.WarRoom.Enabled {
		out.Room = response.People(cat, a, as, pol.WarRoom.Members, pol.WarRoom.UserIDs, nil)
	}
	return out, nil
}

func (a *App) registerResponse(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/response", a.authed(a.can("response:view", a.responseView)))
	mux.HandleFunc("PUT /api/response", a.authed(a.can("response:edit", a.responseSave)))
	mux.HandleFunc("PUT /api/response/jira", a.authed(a.can("response:edit", a.responseJira)))
	mux.HandleFunc("PUT /api/response/graph", a.authed(a.can("response:edit", a.responseGraph)))
	mux.HandleFunc("PUT /api/response/zoom", a.authed(a.can("response:edit", a.responseZoom)))
	mux.HandleFunc("POST /api/response/test/{kind}", a.authed(a.can("response:test", a.responseTest)))
	mux.HandleFunc("POST /api/response/simulate", a.authed(a.can("response:view", a.responseSimulate)))
	mux.HandleFunc("PUT /api/response/voice", a.authed(a.can("response:edit", a.responseVoice)))
	mux.HandleFunc("GET /api/response/voice/preview", a.authed(a.can("response:view", a.responseVoicePreview)))
	mux.HandleFunc("POST /api/response/voice/test", a.authed(a.can("response:test", a.responseVoiceTest)))
	mux.HandleFunc("POST /api/incidents/{id}/response/call", a.authed(a.can("incidents:ack", a.incidentCall)))
	// Microsoft fetches the audio of a Teams call and reports the call; the token of the call in
	// the address is the only key.
	mux.HandleFunc("GET /api/voice/audio/{token}/{file}", a.voiceAudio)
	mux.HandleFunc("POST /api/voice/teams/{token}", a.voiceTeams)
	mux.HandleFunc("GET /api/incidents/{id}/response", a.authed(a.can("incidents:view", a.incidentResponse)))
	mux.HandleFunc("POST /api/incidents/{id}/response/{action}", a.authed(a.can("incidents:ack", a.incidentResponseAct)))
}

func (a *App) responseView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.responseSettings.View())
}

func (a *App) responseSave(w http.ResponseWriter, r *http.Request) {
	var in ResponsePolicyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.responseSettings.SavePolicy(current(r).user.Username, in)
	settingsRespond(w, out, err)
}

func (a *App) responseJira(w http.ResponseWriter, r *http.Request) {
	var in JiraInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.responseSettings.SaveJira(r.Context(), current(r).user.Username, in)
	settingsRespond(w, out, err)
}

func (a *App) responseGraph(w http.ResponseWriter, r *http.Request) {
	var in GraphInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.responseSettings.SaveGraph(r.Context(), current(r).user.Username, in)
	settingsRespond(w, out, err)
}

func (a *App) responseZoom(w http.ResponseWriter, r *http.Request) {
	var in ZoomAPIInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.responseSettings.SaveZoom(r.Context(), current(r).user.Username, in)
	settingsRespond(w, out, err)
}

func (a *App) responseTest(w http.ResponseWriter, r *http.Request) {
	if a.response == nil {
		writeError(w, errResponseUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var info string
	var err error
	switch r.PathValue("kind") {
	case "jira":
		info, err = a.response.TestJira(ctx)
	case "graph":
		info, err = a.response.TestGraph(ctx)
	case "zoom":
		info, err = a.response.TestZoom(ctx)
	default:
		writeError(w, ErrNotFound)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "integration_failed", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}

func (a *App) responseSimulate(w http.ResponseWriter, r *http.Request) {
	var in SimulateInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.responseSettings.Simulate(in)
	settingsRespond(w, out, err)
}

type incidentResponseView struct {
	Mode  string          `json:"mode"`
	State *response.State `json:"state"`
	// Voice is the mode of voice calls; Calls are the voice calls of the incident.
	Voice string               `json:"voice"`
	Calls []response.VoiceCall `json:"calls"`
}

func (a *App) incidentResponseOf(r *http.Request, id string, st *response.State) (incidentResponseView, error) {
	v := a.responseSettings.View()
	calls, err := a.response.Calls(r.Context(), id)
	return incidentResponseView{Mode: v.Mode, Voice: v.Voice.Mode, State: st, Calls: calls}, err
}

func (a *App) responseVoice(w http.ResponseWriter, r *http.Request) {
	var in VoiceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.responseSettings.SaveVoice(r.Context(), current(r).user.Username, in)
	settingsRespond(w, out, err)
}

// responseVoicePreview: format=text is the text of a template for a sample incident, otherwise
// its audio (WAV).
func (a *App) responseVoicePreview(w http.ResponseWriter, r *http.Request) {
	if a.response == nil {
		writeError(w, errResponseUnavailable)
		return
	}
	q := r.URL.Query()
	tmpl := q.Get("template")
	if len([]rune(tmpl)) > model.MaxVoiceTemplate {
		writeError(w, invalid("template_too_long", nil))
		return
	}
	if q.Get("format") == "text" {
		httpx.JSON(w, http.StatusOK, map[string]string{"text": a.response.PreviewText(q.Get("locale"), tmpl, q.Get("via"))})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	wav, err := a.response.Preview(ctx, q.Get("locale"), tmpl, q.Get("via"))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "integration_failed", err)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(wav)
}

type voiceTestInput struct {
	Via string `json:"via"`
}

// responseVoiceTest speaks a test text to the user who asks, by one channel.
func (a *App) responseVoiceTest(w http.ResponseWriter, r *http.Request) {
	if a.response == nil {
		writeError(w, errResponseUnavailable)
		return
	}
	var in voiceTestInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	c, err := a.response.TestCall(ctx, in.Via, current(r).user)
	if errors.Is(err, response.ErrVoiceVia) {
		writeError(w, err)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "integration_failed", err)
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

type incidentCallInput struct {
	Via     string   `json:"via"`
	UserIDs []string `json:"user_ids"`
}

// incidentCall speaks the incident now to the chosen people (the route when none is chosen).
func (a *App) incidentCall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.incidentAllowed(r, id); err != nil {
		writeError(w, err)
		return
	}
	var in incidentCallInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if len(in.UserIDs) > 50 {
		writeError(w, invalid("too_many_people", nil))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if _, err := a.response.Call(ctx, id, in.Via, in.UserIDs, current(r).user.Username); err != nil {
		writeError(w, err)
		return
	}
	st, err := a.response.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	out, err := a.incidentResponseOf(r, id, st)
	settingsRespond(w, out, err)
}

var voiceToken = regexp.MustCompile(`^[0-9a-f]{48}$`)

func (a *App) voiceAudio(w http.ResponseWriter, r *http.Request) {
	token, file := r.PathValue("token"), r.PathValue("file")
	if a.response == nil || !voiceToken.MatchString(token) || (file != "main.wav" && file != "confirm.wav") {
		writeError(w, ErrNotFound)
		return
	}
	data, err := a.response.Audio(r.Context(), token, strings.TrimSuffix(file, ".wav"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (a *App) voiceTeams(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if a.response == nil || !voiceToken.MatchString(token) {
		writeError(w, ErrNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, invalid("body_invalid", err))
		return
	}
	if err := a.response.TeamsEvent(r.Context(), token, body); err != nil {
		if errors.Is(err, response.ErrVoiceGone) {
			writeError(w, err)
			return
		}
		slog.Warn("Teams call notification not handled", "err", err)
		httpx.Error(w, http.StatusBadRequest, "notification_invalid", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// incidentAllowed checks that the incident exists and is in the scope of the user.
func (a *App) incidentAllowed(r *http.Request, id string) error {
	if a.alerts == nil {
		return errResponseUnavailable
	}
	al, _, err := a.alerts.Get(r.Context(), id)
	if err != nil {
		return err
	}
	if !al.InScope(a.incidentScope(current(r).user)) {
		return alert.ErrNotFound
	}
	return nil
}

func (a *App) incidentResponse(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.incidentAllowed(r, id); err != nil {
		writeError(w, err)
		return
	}
	st, err := a.response.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	out, err := a.incidentResponseOf(r, id, st)
	settingsRespond(w, out, err)
}

func (a *App) incidentResponseAct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.incidentAllowed(r, id); err != nil {
		writeError(w, err)
		return
	}
	action := r.PathValue("action")
	if action == "assess" {
		action = ""
	}
	st, err := a.response.Force(r.Context(), id, action, current(r).user.Username)
	if err != nil {
		writeError(w, err)
		return
	}
	out, err := a.incidentResponseOf(r, id, st)
	settingsRespond(w, out, err)
}
