package model

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Impact levels of an incident on the business: how much of the business its services carry.
const (
	ImpactExtensive   = "extensive"
	ImpactSignificant = "significant"
	ImpactModerate    = "moderate"
	ImpactMinor       = "minor"
)

// Impacts are the impact levels, the widest first.
var Impacts = []string{ImpactExtensive, ImpactSignificant, ImpactModerate, ImpactMinor}

// ImpactRank orders impact levels: the widest ranks highest, unknown ones rank 0.
func ImpactRank(v string) int {
	if i := slices.Index(Impacts, v); i >= 0 {
		return len(Impacts) - i
	}
	return 0
}

// RaiseImpact is the impact one level wider; the widest stays.
func RaiseImpact(v string) string {
	i := slices.Index(Impacts, v)
	if i <= 0 {
		return Impacts[0]
	}
	return Impacts[i-1]
}

// Integration modes: off, dry run (everything is planned and recorded, nothing is sent) and live.
const (
	ModeOff    = "off"
	ModeDryRun = "dry_run"
	ModeLive   = "live"
)

func ValidMode(v string) bool { return v == ModeOff || v == ModeDryRun || v == ModeLive }

// Communication methods of a response policy. Email, Telegram, Teams and Zoom are messages
// through the backup notification channels (people and team channels); war_room is the incident
// chat in Microsoft Teams; call_teams and call_zoom are a conference call (an online meeting)
// whose link every message carries. voice_teams, voice_telegram and voice_zoom speak the
// incident to each person in their interface language: a Teams call, a Telegram voice message
// (bots cannot call), a voice message in Zoom Team Chat (Zoom has no API to place a call).
const (
	CommEmail     = "email"
	CommTelegram  = "telegram"
	CommTeams     = "teams"
	CommZoom      = "zoom"
	CommWarRoom   = "war_room"
	CommCallTeams = "call_teams"
	CommCallZoom  = "call_zoom"
	// CommPagerDuty sends the incident to PagerDuty at this step, whatever the PagerDuty mode
	// of its severity says (an incident PagerDuty already has stays as it is).
	CommPagerDuty = "pagerduty"
	// Voice methods: the incident spoken by text-to-speech.
	CommVoiceTeams    = "voice_teams"
	CommVoiceTelegram = "voice_telegram"
	CommVoiceZoom     = "voice_zoom"
)

var CommMethods = []string{CommEmail, CommTelegram, CommTeams, CommZoom, CommWarRoom, CommCallTeams, CommCallZoom, CommPagerDuty, CommVoiceTeams, CommVoiceTelegram, CommVoiceZoom}

// VoiceVia is the voice channel of a voice method ("" for another method).
func VoiceVia(method string) string {
	switch method {
	case CommVoiceTeams:
		return VoiceTeams
	case CommVoiceTelegram:
		return VoiceTelegram
	case CommVoiceZoom:
		return VoiceZoom
	}
	return ""
}

// Voice channels.
const (
	VoiceTeams    = "teams"
	VoiceTelegram = "telegram"
	VoiceZoom     = "zoom"
)

func ValidVoiceVia(v string) bool { return v == VoiceTeams || v == VoiceTelegram || v == VoiceZoom }

func ValidComm(v string) bool { return slices.Contains(CommMethods, v) }

// Escalation targets: who a step reaches.
const (
	// TargetRoute: the people of the incident route (the owning team, or the owners of the item)
	// and the team channel.
	TargetRoute = "route"
	// TargetLead: the lead of the owning team.
	TargetLead = "lead"
	// TargetParentLead: the lead of the parent team of the owning team (the manager).
	TargetParentLead = "parent_lead"
	// TargetOwners: the people responsible for the configuration item.
	TargetOwners = "ci_owners"
	// TargetServiceOwners: the leads of the teams owning the affected business services,
	// dependent services included.
	TargetServiceOwners = "service_owners"
)

var Targets = []string{TargetRoute, TargetLead, TargetParentLead, TargetOwners, TargetServiceOwners}

func ValidTarget(v string) bool { return slices.Contains(Targets, v) }

// Response is incident response: the impact of an incident on business services decides its
// priority, and the policy of that priority decides how people are told and called, how the
// incident escalates, whether a war room chat is opened in Microsoft Teams and whether Jira
// gets a task and a postmortem.
type Response struct {
	// Mode: off, dry_run (everything is assessed, planned and written on the timeline, nothing is
	// sent anywhere) or live.
	Mode string `json:"mode"`
	// ActiveSince: when the mode last left off. Incidents opened before are not handled, so
	// turning response on does not escalate every old incident at once.
	ActiveSince *time.Time       `json:"active_since,omitempty"`
	Impact      ImpactPolicy     `json:"impact"`
	Policies    []ResponsePolicy `json:"policies"`
	Jira        JiraSettings     `json:"jira"`
	Graph       GraphSettings    `json:"graph"`
	ZoomAPI     ZoomAPISettings  `json:"zoom_api"`
	Voice       VoiceSettings    `json:"voice"`
	// UpdatedAt and UpdatedBy: the last change of the policies.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// ImpactPolicy turns what an incident touches into its impact, and impact with the severity of
// the events (the urgency) into the priority of the response.
type ImpactPolicy struct {
	// Criticality maps the criticality of the most critical affected service to an impact.
	Criticality map[string]string `json:"criticality"`
	// NoService is the impact of an incident whose item belongs to no business service.
	NoService string `json:"no_service"`
	// RaiseRED: a RED signal (rate, errors, duration) is what users see, so it widens impact by one level.
	RaiseRED bool `json:"raise_red"`
	// RaiseDependents: when at least this many other services depend on the affected ones, impact
	// widens by one level; 0 is off.
	RaiseDependents int `json:"raise_dependents"`
	// RaiseIncidents: when at least this many active incidents hit the same services, impact
	// widens by one level (a mass failure); 0 is off.
	RaiseIncidents int `json:"raise_incidents"`
	// Matrix: impact -> severity of the events -> priority (a severity name).
	Matrix map[string]map[string]string `json:"matrix"`
	// NeverLower: the priority is never lower than the severity of the events.
	NeverLower bool `json:"never_lower"`
}

// DefaultImpactPolicy is the ITIL-like default: the criticality of the service is its impact,
// and the matrix crosses impact with urgency.
func DefaultImpactPolicy() ImpactPolicy {
	m := map[string]map[string]string{
		ImpactExtensive:   {SeverityCritical: SeverityCritical, SeverityError: SeverityCritical, SeverityWarning: SeverityError, SeverityLow: SeverityWarning, SeverityInfo: SeverityLow},
		ImpactSignificant: {SeverityCritical: SeverityCritical, SeverityError: SeverityError, SeverityWarning: SeverityError, SeverityLow: SeverityWarning, SeverityInfo: SeverityLow},
		ImpactModerate:    {SeverityCritical: SeverityError, SeverityError: SeverityError, SeverityWarning: SeverityWarning, SeverityLow: SeverityLow, SeverityInfo: SeverityInfo},
		ImpactMinor:       {SeverityCritical: SeverityError, SeverityError: SeverityWarning, SeverityWarning: SeverityLow, SeverityLow: SeverityLow, SeverityInfo: SeverityInfo},
	}
	return ImpactPolicy{
		Criticality: map[string]string{CriticalityCritical: ImpactExtensive, CriticalityHigh: ImpactSignificant, CriticalityMedium: ImpactModerate, CriticalityLow: ImpactMinor},
		NoService:   ImpactModerate, RaiseRED: true, RaiseDependents: 3, RaiseIncidents: 5, Matrix: m,
	}
}

// Validate checks the policy and fills what is missing from the default.
func (p *ImpactPolicy) Validate() error {
	def := DefaultImpactPolicy()
	if p.Criticality == nil {
		p.Criticality = map[string]string{}
	}
	for c := range p.Criticality {
		if !ValidCriticality(c) {
			return fmt.Errorf("unknown criticality %q", c)
		}
	}
	for _, c := range Criticalities {
		v, ok := p.Criticality[c]
		if !ok || v == "" {
			p.Criticality[c] = def.Criticality[c]
		} else if ImpactRank(v) == 0 {
			return fmt.Errorf("unknown impact %q", v)
		}
	}
	if p.NoService == "" {
		p.NoService = def.NoService
	} else if ImpactRank(p.NoService) == 0 {
		return fmt.Errorf("unknown impact %q", p.NoService)
	}
	if p.RaiseDependents < 0 || p.RaiseDependents > 1000 || p.RaiseIncidents < 0 || p.RaiseIncidents > 1000 {
		return errors.New("thresholds must be between 0 and 1000")
	}
	if p.Matrix == nil {
		p.Matrix = map[string]map[string]string{}
	}
	for imp, row := range p.Matrix {
		if ImpactRank(imp) == 0 {
			return fmt.Errorf("unknown impact %q", imp)
		}
		for sev, pr := range row {
			if !ValidSeverity(sev) || !ValidSeverity(pr) {
				return fmt.Errorf("unknown severity in the matrix: %q -> %q", sev, pr)
			}
		}
	}
	for _, imp := range Impacts {
		if p.Matrix[imp] == nil {
			p.Matrix[imp] = map[string]string{}
		}
		for _, sev := range SeverityNames() {
			if p.Matrix[imp][sev] == "" {
				p.Matrix[imp][sev] = def.Matrix[imp][sev]
			}
		}
	}
	return nil
}

// ResponsePolicy is how incidents of one priority are handled.
type ResponsePolicy struct {
	// Priority is the severity name the policy is for (critical is P1 … info is P5).
	Priority string `json:"priority"`
	// Enabled: incidents of this priority are handled by the policy; otherwise only by backup
	// notification as before.
	Enabled bool `json:"enabled"`
	// Steps are the notification and escalation steps, by time since the incident opened.
	Steps []EscalationStep `json:"steps"`
	// StopOnAck: the escalation stops once the incident is acknowledged; otherwise it goes on
	// until the incident is resolved.
	StopOnAck bool `json:"stop_on_ack"`
	// WarRoom opens a Microsoft Teams chat for the incident.
	WarRoom WarRoomPolicy `json:"war_room"`
	// Bridge is the conference call of the incident: "" (none), teams or zoom.
	Bridge string `json:"bridge"`
	// Jira: a task to resolve the incident and a postmortem.
	Jira JiraPolicy `json:"jira"`
}

// EscalationStep reaches its targets by its methods AfterMinutes after the incident opened.
type EscalationStep struct {
	AfterMinutes int      `json:"after_minutes"`
	Targets      []string `json:"targets"`
	// UserIDs and TeamIDs are more people and teams (their lead and channel) to reach.
	UserIDs []string `json:"user_ids"`
	TeamIDs []string `json:"team_ids"`
	// Methods are the communication methods (CommMethods).
	Methods []string `json:"methods"`
}

// WarRoomPolicy: the chat of the incident and who is in it.
type WarRoomPolicy struct {
	Enabled bool `json:"enabled"`
	// Members: the targets added to the chat (TargetRoute, TargetLead…), plus UserIDs.
	Members []string `json:"members"`
	UserIDs []string `json:"user_ids"`
	// AddEscalated: people reached by later escalation steps join the chat too.
	AddEscalated bool `json:"add_escalated"`
	// PostUpdates: acknowledgement, a raised priority, resolution and the postmortem are
	// posted in the chat.
	PostUpdates bool `json:"post_updates"`
}

// JiraPolicy: what Jira gets for incidents of the priority.
type JiraPolicy struct {
	// Task: an issue to resolve the incident, created when the incident opens.
	Task bool `json:"task"`
	// Postmortem: an issue to analyse the incident and prevent it from recurring, created when
	// the incident is resolved, due PostmortemDays later.
	Postmortem     bool `json:"postmortem"`
	PostmortemDays int  `json:"postmortem_days"`
	// Comment: status changes of the incident are added to the task as comments.
	Comment bool `json:"comment"`
}

// DefaultResponsePolicies are the policies of a new installation: P1 and P2 escalate, open a
// war room, a bridge call and Jira issues; P3 notifies and creates a task; P4 and P5 only notify.
func DefaultResponsePolicies() []ResponsePolicy {
	ps := []ResponsePolicy{
		{Priority: SeverityCritical, Enabled: true, StopOnAck: false, Bridge: "teams", Steps: []EscalationStep{
			{AfterMinutes: 0, Targets: []string{TargetRoute}, Methods: []string{CommEmail, CommTelegram, CommTeams, CommWarRoom, CommCallTeams}},
			{AfterMinutes: 10, Targets: []string{TargetLead, TargetServiceOwners}, Methods: []string{CommEmail, CommTelegram, CommWarRoom}},
			{AfterMinutes: 30, Targets: []string{TargetParentLead}, Methods: []string{CommEmail, CommTelegram, CommWarRoom}},
		}, WarRoom: WarRoomPolicy{Enabled: true, Members: []string{TargetRoute, TargetLead, TargetServiceOwners}, AddEscalated: true, PostUpdates: true},
			Jira: JiraPolicy{Task: true, Postmortem: true, PostmortemDays: 5, Comment: true}},
		{Priority: SeverityError, Enabled: true, StopOnAck: true, Bridge: "", Steps: []EscalationStep{
			{AfterMinutes: 0, Targets: []string{TargetRoute}, Methods: []string{CommEmail, CommTelegram, CommTeams, CommWarRoom}},
			{AfterMinutes: 15, Targets: []string{TargetLead}, Methods: []string{CommEmail, CommTelegram, CommWarRoom}},
			{AfterMinutes: 60, Targets: []string{TargetParentLead}, Methods: []string{CommEmail}},
		}, WarRoom: WarRoomPolicy{Enabled: true, Members: []string{TargetRoute, TargetLead}, AddEscalated: true, PostUpdates: true},
			Jira: JiraPolicy{Task: true, Postmortem: true, PostmortemDays: 10, Comment: true}},
		{Priority: SeverityWarning, Enabled: true, StopOnAck: true, Steps: []EscalationStep{
			{AfterMinutes: 0, Targets: []string{TargetRoute}, Methods: []string{CommEmail, CommTeams}},
			{AfterMinutes: 60, Targets: []string{TargetLead}, Methods: []string{CommEmail}},
		}, WarRoom: WarRoomPolicy{Members: []string{TargetRoute}, PostUpdates: true}, Jira: JiraPolicy{Task: true, PostmortemDays: 10}},
		{Priority: SeverityLow, Enabled: true, StopOnAck: true, Steps: []EscalationStep{
			{AfterMinutes: 0, Targets: []string{TargetRoute}, Methods: []string{CommEmail}},
		}, WarRoom: WarRoomPolicy{Members: []string{TargetRoute}}, Jira: JiraPolicy{PostmortemDays: 10}},
		{Priority: SeverityInfo, Enabled: false, StopOnAck: true, WarRoom: WarRoomPolicy{Members: []string{TargetRoute}}, Jira: JiraPolicy{PostmortemDays: 10}},
	}
	for i := range ps {
		ps[i].fillLists()
	}
	return ps
}

// fillLists turns lists that were never set into empty ones, so the web interface gets [] and
// not null: the defaults leave out the people and teams of a step, and settings saved before a
// list existed have none.
func (p *ResponsePolicy) fillLists() {
	nonNil := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	if p.Steps == nil {
		p.Steps = []EscalationStep{}
	}
	for i := range p.Steps {
		s := &p.Steps[i]
		s.Targets, s.UserIDs, s.TeamIDs, s.Methods = nonNil(s.Targets), nonNil(s.UserIDs), nonNil(s.TeamIDs), nonNil(s.Methods)
	}
	p.WarRoom.Members, p.WarRoom.UserIDs = nonNil(p.WarRoom.Members), nonNil(p.WarRoom.UserIDs)
}

const (
	MaxEscalationSteps = 10
	MaxStepMinutes     = 7 * 24 * 60
	MaxPostmortemDays  = 90
	maxStepPeople      = 50
)

// NormalizePolicies checks the policies of a form and returns one per priority, most severe
// first; a priority the form leaves out gets a disabled policy.
func NormalizePolicies(in []ResponsePolicy) ([]ResponsePolicy, error) {
	by := map[string]ResponsePolicy{}
	for _, p := range in {
		if !ValidSeverity(p.Priority) {
			return nil, fmt.Errorf("unknown priority %q", p.Priority)
		}
		if _, dup := by[p.Priority]; dup {
			return nil, fmt.Errorf("priority %q is given twice", p.Priority)
		}
		if err := p.normalize(); err != nil {
			return nil, fmt.Errorf("%s: %w", SeverityPriority(p.Priority), err)
		}
		by[p.Priority] = p
	}
	out := make([]ResponsePolicy, 0, len(Severities))
	for _, s := range Severities {
		p, ok := by[s.Name]
		if !ok {
			p = ResponsePolicy{Priority: s.Name, StopOnAck: true, Steps: []EscalationStep{}, WarRoom: WarRoomPolicy{Members: []string{}, UserIDs: []string{}}, Jira: JiraPolicy{PostmortemDays: 10}}
		}
		out = append(out, p)
	}
	return out, nil
}

func cleanIDs(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func (p *ResponsePolicy) normalize() error {
	switch p.Bridge {
	case "", "teams", "zoom":
	default:
		return fmt.Errorf("unknown bridge %q", p.Bridge)
	}
	if len(p.Steps) > MaxEscalationSteps {
		return fmt.Errorf("more than %d steps", MaxEscalationSteps)
	}
	if p.Steps == nil {
		p.Steps = []EscalationStep{}
	}
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.AfterMinutes < 0 || s.AfterMinutes > MaxStepMinutes {
			return fmt.Errorf("step %d: the delay must be between 0 and %d minutes", i+1, MaxStepMinutes)
		}
		s.Targets, s.UserIDs, s.TeamIDs, s.Methods = cleanIDs(s.Targets), cleanIDs(s.UserIDs), cleanIDs(s.TeamIDs), cleanIDs(s.Methods)
		for _, t := range s.Targets {
			if !ValidTarget(t) {
				return fmt.Errorf("step %d: unknown target %q", i+1, t)
			}
		}
		for _, m := range s.Methods {
			if !ValidComm(m) {
				return fmt.Errorf("step %d: unknown method %q", i+1, m)
			}
		}
		if len(s.UserIDs)+len(s.TeamIDs) > maxStepPeople {
			return fmt.Errorf("step %d: more than %d people and teams", i+1, maxStepPeople)
		}
	}
	slices.SortStableFunc(p.Steps, func(a, b EscalationStep) int { return a.AfterMinutes - b.AfterMinutes })
	p.WarRoom.Members, p.WarRoom.UserIDs = cleanIDs(p.WarRoom.Members), cleanIDs(p.WarRoom.UserIDs)
	for _, t := range p.WarRoom.Members {
		if !ValidTarget(t) {
			return fmt.Errorf("war room: unknown member %q", t)
		}
	}
	if p.Jira.PostmortemDays == 0 {
		p.Jira.PostmortemDays = 10
	}
	if p.Jira.PostmortemDays < 1 || p.Jira.PostmortemDays > MaxPostmortemDays {
		return fmt.Errorf("the postmortem is due in 1 to %d days", MaxPostmortemDays)
	}
	return nil
}

// PolicyFor is the policy of a priority; ok is false when there is none or it is off.
func (r Response) PolicyFor(priority string) (ResponsePolicy, bool) {
	for _, p := range r.Policies {
		if p.Priority == priority {
			return p, p.Enabled
		}
	}
	return ResponsePolicy{}, false
}

// JiraSettings connect Jira Cloud: issues are created in Project by the account Email with an
// API token kept in OpenBao.
type JiraSettings struct {
	Mode string `json:"mode"`
	// BaseURL is the site, for example https://example.atlassian.net.
	BaseURL  string `json:"base_url"`
	Email    string `json:"email"`
	TokenRef string `json:"-"`
	// Project is the project key, for example OPS.
	Project string `json:"project"`
	// TaskType and PostmortemType are issue type names (Task, Bug, Incident…).
	TaskType       string `json:"task_type"`
	PostmortemType string `json:"postmortem_type"`
	// Priorities maps a severity name to a Jira priority name; empty leaves the Jira default.
	Priorities map[string]string `json:"priorities"`
	Labels     []string          `json:"labels"`
	// LinkType is the issue link type between the postmortem and the task ("Relates").
	LinkType string `json:"link_type"`
	// DoneTransition: the transition applied to the task when the incident is resolved
	// ("Done"); empty leaves the task as it is.
	DoneTransition string `json:"done_transition"`
}

// DefaultJira is the Jira part of a new installation.
func DefaultJira() JiraSettings {
	return JiraSettings{Mode: ModeOff, TaskType: "Task", PostmortemType: "Task", LinkType: "Relates", Labels: []string{"umbrella", "incident"},
		Priorities: map[string]string{SeverityCritical: "Highest", SeverityError: "High", SeverityWarning: "Medium", SeverityLow: "Low", SeverityInfo: "Lowest"}}
}

// API is the REST address of the site.
func (j JiraSettings) API() string { return strings.TrimRight(j.BaseURL, "/") }

// GraphSettings connect Microsoft Teams through Microsoft Graph with a service account: an app
// registration (TenantID, ClientID and its secret) and the refresh token of the account it acts
// as (delegated permissions Chat.Create, ChatMessage.Send, ChatMember.ReadWrite,
// OnlineMeetings.ReadWrite, User.ReadBasic.All, offline_access). Secrets are in OpenBao.
type GraphSettings struct {
	Mode            string `json:"mode"`
	TenantID        string `json:"tenant_id"`
	ClientID        string `json:"client_id"`
	ClientSecretRef string `json:"-"`
	RefreshTokenRef string `json:"-"`
	// Account is the user principal name of the service account, shown and added to chats.
	Account string `json:"account"`
	// LoginURL and GraphURL replace https://login.microsoftonline.com and
	// https://graph.microsoft.com (a proxy, national clouds, tests).
	LoginURL string `json:"login_url,omitempty"`
	GraphURL string `json:"graph_url,omitempty"`
}

func (g GraphSettings) Login() string {
	if g.LoginURL != "" {
		return strings.TrimRight(g.LoginURL, "/")
	}
	return "https://login.microsoftonline.com"
}

func (g GraphSettings) API() string {
	if g.GraphURL != "" {
		return strings.TrimRight(g.GraphURL, "/")
	}
	return "https://graph.microsoft.com/v1.0"
}

// ZoomAPISettings connect Zoom meetings through a Server-to-Server OAuth app: the bridge call of
// an incident is a meeting of User (an e-mail or "me").
type ZoomAPISettings struct {
	Mode            string `json:"mode"`
	AccountID       string `json:"account_id"`
	ClientID        string `json:"client_id"`
	ClientSecretRef string `json:"-"`
	User            string `json:"user"`
	// OAuthURL and APIURL replace https://zoom.us and https://api.zoom.us/v2 (tests).
	OAuthURL string `json:"oauth_url,omitempty"`
	APIURL   string `json:"api_url,omitempty"`
}

func (z ZoomAPISettings) OAuth() string {
	if z.OAuthURL != "" {
		return strings.TrimRight(z.OAuthURL, "/")
	}
	return "https://zoom.us"
}

func (z ZoomAPISettings) API() string {
	if z.APIURL != "" {
		return strings.TrimRight(z.APIURL, "/")
	}
	return "https://api.zoom.us/v2"
}

// Text-to-speech engines. All of them are self-hosted servers: Piper (its HTTP server),
// RHVoice (rhvoice-rest) and any server with the OpenAI speech API (openedai-speech, LocalAI,
// Kokoro-FastAPI…).
const (
	TTSPiper   = "piper"
	TTSRHVoice = "rhvoice"
	TTSOpenAI  = "openai"
)

func ValidTTS(v string) bool { return v == TTSPiper || v == TTSRHVoice || v == TTSOpenAI }

// VoiceSettings: incidents spoken to people. The text comes from a template of the language of
// the person, a text-to-speech server turns it into audio.
type VoiceSettings struct {
	// Mode: off, dry_run (calls are planned and written on the timeline, nothing is sent) or live.
	Mode string      `json:"mode"`
	TTS  TTSSettings `json:"tts"`
	// Templates by locale; an empty one is the built-in text. Placeholders are in {braces}.
	Templates map[string]string `json:"templates"`
	// Repeat: how many times a call speaks the text (1–3).
	Repeat int `json:"repeat"`
	// AckDigit: in a Teams call, pressing 1 acknowledges the incident.
	AckDigit bool `json:"ack_digit"`
}

// TTSSettings connect the text-to-speech server.
type TTSSettings struct {
	Provider string `json:"provider"`
	// URL of the server: Piper http://piper:5000, rhvoice-rest http://rhvoice:8080, an OpenAI
	// speech API http://tts:8000 (/v1/audio/speech is added).
	URL string `json:"url"`
	// Voices by locale (en, ru): a Piper voice (ru_RU-irina-medium), an RHVoice voice (anna) or a
	// voice of the OpenAI server.
	Voices map[string]string `json:"voices"`
	// Model of the OpenAI speech API (tts-1).
	Model     string `json:"model"`
	APIKeyRef string `json:"-"`
}

// DefaultVoice is the voice part of a new installation: Piper with a Russian and an English voice.
func DefaultVoice() VoiceSettings {
	return VoiceSettings{Mode: ModeOff, Repeat: 2, AckDigit: true, Templates: map[string]string{},
		TTS: TTSSettings{Provider: TTSPiper, URL: "http://piper:5000", Voices: map[string]string{LocaleRU: "ru_RU-irina-medium", LocaleEN: "en_US-lessac-medium"}}}
}

// DefaultVoiceTemplates are the built-in texts of voice calls.
var DefaultVoiceTemplates = map[string]string{
	LocaleRU: "Внимание! Инцидент Umbrella номер {number}. Приоритет {priority}. {title}. Объект: {ci}. Затронутые сервисы: {services}. Команда: {team}. Открыт в {opened}. Статус: {status}. {ack_hint}",
	LocaleEN: "Attention! Umbrella incident number {number}. Priority {priority}. {title}. Item: {ci}. Affected services: {services}. Team: {team}. Opened at {opened}. Status: {status}. {ack_hint}",
}

// VoicePlaceholders are what voice templates may use.
var VoicePlaceholders = []string{"number", "id", "priority", "title", "ci", "services", "team", "opened", "status", "impact", "level", "signal", "ack_hint"}

// MaxVoiceTemplate is the longest template, in characters.
const MaxVoiceTemplate = 2000

// Template is the template of a locale: the saved one or the built-in one.
func (v VoiceSettings) Template(locale string) string {
	if t := strings.TrimSpace(v.Templates[locale]); t != "" {
		return t
	}
	if t, ok := DefaultVoiceTemplates[locale]; ok {
		return t
	}
	return DefaultVoiceTemplates[LocaleEN]
}

// NormalizeHTTPS checks a site address: https (http only for a loopback test server), no query.
func NormalizeHTTPS(v string) (string, error) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the address must be a URL without a query")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return "", errors.New("the address must be https")
	}
	return v, nil
}

func isLoopback(h string) bool { return h == "127.0.0.1" || h == "localhost" || h == "::1" }

// Effective is the response settings with the defaults of what was never saved: the impact
// policy, the policies of each priority and the Jira defaults.
func (r Response) Effective() Response {
	r = r.Clone()
	if r.Mode == "" {
		r.Mode = ModeOff
	}
	if r.Impact.Matrix == nil {
		r.Impact = DefaultImpactPolicy()
	} else {
		_ = r.Impact.Validate()
	}
	if r.Policies == nil {
		r.Policies = DefaultResponsePolicies()
	}
	for i := range r.Policies {
		r.Policies[i].fillLists()
	}
	def := DefaultJira()
	if r.Jira.Mode == "" {
		r.Jira.Mode = ModeOff
	}
	if r.Jira.TaskType == "" {
		r.Jira.TaskType = def.TaskType
	}
	if r.Jira.PostmortemType == "" {
		r.Jira.PostmortemType = def.PostmortemType
	}
	if r.Jira.LinkType == "" {
		r.Jira.LinkType = def.LinkType
	}
	if r.Jira.Priorities == nil {
		r.Jira.Priorities = def.Priorities
	}
	if r.Jira.Labels == nil {
		r.Jira.Labels = def.Labels
	}
	if r.Graph.Mode == "" {
		r.Graph.Mode = ModeOff
	}
	if r.ZoomAPI.Mode == "" {
		r.ZoomAPI.Mode = ModeOff
	}
	if r.ZoomAPI.User == "" {
		r.ZoomAPI.User = "me"
	}
	dv := DefaultVoice()
	if r.Voice.Mode == "" {
		r.Voice.Mode = ModeOff
	}
	if r.Voice.TTS.Provider == "" {
		r.Voice.TTS = dv.TTS
		r.Voice.AckDigit = true
	}
	if r.Voice.Repeat <= 0 {
		r.Voice.Repeat = dv.Repeat
	}
	if r.Voice.TTS.Voices == nil {
		r.Voice.TTS.Voices = map[string]string{}
	}
	if r.Voice.Templates == nil {
		r.Voice.Templates = map[string]string{}
	}
	return r
}

// Clone is a deep copy: the store keeps the original.
func (r Response) Clone() Response {
	r.Impact.Criticality = maps.Clone(r.Impact.Criticality)
	if r.Impact.Matrix != nil {
		m := make(map[string]map[string]string, len(r.Impact.Matrix))
		for k, v := range r.Impact.Matrix {
			m[k] = maps.Clone(v)
		}
		r.Impact.Matrix = m
	}
	if r.Policies != nil {
		ps := make([]ResponsePolicy, len(r.Policies))
		for i, p := range r.Policies {
			steps := make([]EscalationStep, len(p.Steps))
			for j, s := range p.Steps {
				s.Targets, s.UserIDs, s.TeamIDs, s.Methods = slices.Clone(s.Targets), slices.Clone(s.UserIDs), slices.Clone(s.TeamIDs), slices.Clone(s.Methods)
				steps[j] = s
			}
			p.Steps = steps
			p.WarRoom.Members, p.WarRoom.UserIDs = slices.Clone(p.WarRoom.Members), slices.Clone(p.WarRoom.UserIDs)
			ps[i] = p
		}
		r.Policies = ps
	}
	r.Jira.Priorities = maps.Clone(r.Jira.Priorities)
	r.Jira.Labels = slices.Clone(r.Jira.Labels)
	r.Voice.Templates = maps.Clone(r.Voice.Templates)
	r.Voice.TTS.Voices = maps.Clone(r.Voice.TTS.Voices)
	return r
}
