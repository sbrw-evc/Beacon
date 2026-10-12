package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/response"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/tgbot"
)

const (
	CookieName = "umbrella_session"
	CSRFHeader = "X-CSRF-Token"

	// maxFailures wrong passwords for one account from one address lock that pair;
	// maxLooseFailures lock the address (any accounts) or the account (any addresses).
	maxFailures      = 10
	maxLooseFailures = 50
	failWindow       = 15 * time.Minute
	lockout          = 5 * time.Minute
)

type Options struct {
	Version       string
	Commit        string
	BuiltAt       string
	SecureCookies bool
	Web           http.Handler
	// TrustedProxies are the reverse proxies whose forwarding headers are believed for the
	// client address (sign-in limits, audit, connector and TV wallboard networks); nil reads
	// UMBRELLA_TRUSTED_PROXIES.
	TrustedProxies TrustedProxies
	// Ingest tunes the intake queue and workers; zero fields are the defaults.
	Ingest ingest.Config
}

type Deps struct {
	Config   config.File
	Vault    *secrets.Client
	Backend  *store.PGBackend
	Store    *store.Store
	Sessions *auth.Sessions
	Runtime  Runtime
}

type App struct {
	opt       Options
	deps      Deps
	auth      *AuthService
	users     *UserService
	accounts  *UsersService
	settings  *SettingsService
	status    *StatusService
	limiter   *loginGuard
	entraRate *Limiter // Microsoft sign-in starts per client address
	policy    *PolicyService
	access    *AccessService
	directory *DirectoryService
	entra     *EntraService
	postgres  *PostgresService
	openbao   *OpenBaoService
	roles     *RolesService
	teams     *TeamsService
	services  *ServicesService
	netbox    *NetBoxService
	// inventory is the optional Inventory DB connection: devices in, alert states out.
	inventory  *InventoryDBService
	cis        *CIService
	cmdb       *CMDBService
	groups     *GroupsService
	monitoring *MonitoringService
	alertPoll  *alertPoller
	// hostContext shows the machine of an incident: graphs, logs and events around it.
	hostContext *HostContextService

	// serviceAccounts are the machine identities that call the API with tokens.
	serviceAccounts *ServiceAccountsService

	creds         *CredentialsService
	connectors    *ConnectorsService
	queue         *ingest.Queue
	alerts        *alert.Engine
	feed          feed
	pdGateway     *pagerduty.Gateway
	pagerduty     *PagerDutyService
	notifier      *notify.Service
	notifications *NotificationsService
	response      *response.Service
	// responseSettings keeps the settings of incident response; response runs it (PostgreSQL only).
	responseSettings *ResponseService
	maintenance      *MaintenanceService
	wallboards       *WallboardService
	proxies          TrustedProxies
	rules            *RulesService
	ruleEngine       *rules.Engine
	// tgBot is the Telegram bot built into Umbrella (PostgreSQL only: it needs the link key).
	tgBot *tgbot.Bot
	ready atomic.Bool
	rates rates
}

func New(opt Options, deps Deps) *App {
	if opt.Web == nil {
		opt.Web = http.NotFoundHandler()
	}
	opt.Ingest = opt.Ingest.WithDefaults()
	passwords := credentials.NewPasswords(deps.Vault)
	dir := ldapDirectory{}
	users := NewUserService(deps.Store, passwords)
	var vault Secrets
	if deps.Vault != nil {
		vault = deps.Vault
	}
	creds := NewCredentialsService(deps.Store, vault)
	nb := NewNetBoxService(deps.Store, vault)
	var queue *ingest.Queue
	var db Database
	if deps.Backend != nil {
		queue = ingest.NewWithConfig(deps.Backend.Pool(), opt.Ingest)
		db = deps.Backend
	}
	var sessions SessionCounter
	if deps.Sessions != nil {
		sessions = deps.Sessions
	}
	a := &App{
		creds:       creds,
		connectors:  NewConnectorsService(deps.Store, creds),
		queue:       queue,
		opt:         opt,
		deps:        deps,
		users:       users,
		accounts:    NewUsersService(deps.Store, passwords, deps.Vault, deps.Sessions),
		auth:        NewAuthService(deps.Store, passwords, deps.Vault, dir, users),
		settings:    NewSettingsService(deps.Store),
		limiter:     newLoginGuard(),
		entraRate:   NewLimiter(entraStartsPerMinute, time.Minute, time.Minute),
		policy:      NewPolicyService(deps.Store),
		access:      NewAccessService(deps.Store),
		directory:   NewDirectoryService(deps.Store, deps.Vault, dir, deps.Sessions),
		entra:       NewEntraService(deps.Store, deps.Vault, users, deps.Sessions),
		postgres:    NewPostgresService(deps.Store, deps.Backend, deps.Vault, deps.Runtime, deps.Config),
		openbao:     NewOpenBaoService(deps.Store, deps.Vault, deps.Runtime, deps.Config),
		roles:       NewRolesService(deps.Store),
		teams:       NewTeamsService(deps.Store),
		services:    NewServicesService(deps.Store, nb),
		netbox:      nb,
		cis:         NewCIService(deps.Store, nb),
		groups:      NewGroupsService(deps.Store, vault, dir),
		maintenance: NewMaintenanceService(deps.Store),
		wallboards:  NewWallboardService(deps.Store),
		proxies:     opt.TrustedProxies,
	}
	if a.proxies == nil {
		a.proxies = ParseTrustedProxies(os.Getenv("UMBRELLA_TRUSTED_PROXIES"))
	}
	var resolver pagerduty.Resolver = noSecrets{}
	if vault != nil {
		resolver = vault
	}
	a.pdGateway = pagerduty.New(deps.Store, resolver)
	a.pagerduty = NewPagerDutyService(deps.Store, vault, a.pdGateway)
	a.pdGateway.SetUsers(usersByEmail(deps.Store))
	a.pdGateway.SetQueueHook(a.pagerduty.autoLinkQueues)
	a.notifier = notify.New(deps.Store, resolver)
	a.notifier.SetOnCall(a.pdGateway)
	a.notifications = NewNotificationsService(deps.Store, vault, a.notifier)
	a.responseSettings = NewResponseService(deps.Store, vault, a.notifier)
	if queue != nil {
		a.alerts = alert.New(deps.Backend.Pool(), deps.Store)
		a.alerts.SetSender(a.pdGateway)
		a.alerts.SetNotifier(a.notifier)
		a.pdGateway.SetResults(a.alerts)
		a.notifier.SetResults(a.alerts)
		queue.SetSink(a.alertSink)
		var sec response.Secrets
		if vault != nil {
			sec = vault
		}
		a.response = response.New(deps.Backend.Pool(), deps.Store, a.alerts, a.notifier, sec)
		a.response.SetPagerDuty(pdResponse{alerts: a.alerts, gw: a.pdGateway})
		a.tgBot = tgbot.New(botBackend{a}, a.botLock)
	}
	a.ruleEngine = rules.New(deps.Store, ruleCredentials(creds))
	a.rules = NewRulesService(deps.Store, a.ruleEngine, creds)
	if a.alerts != nil {
		a.ruleEngine.SetSink(a.alerts)
	}
	var active activeSource
	if a.alerts != nil {
		active = func(ctx context.Context) ([]alert.Alert, error) {
			if !a.ingestReady() {
				return nil, errEventsNotReady
			}
			return a.alerts.ActiveOnCIs(ctx, maxActive)
		}
	}
	a.cmdb = NewCMDBService(deps.Store, active)
	a.serviceAccounts = NewServiceAccountsService(deps.Store, vault)
	a.inventory = NewInventoryDBService(deps.Store, vault, active)
	a.monitoring = NewMonitoringService(deps.Store, creds, a.cis)
	a.alertPoll = newAlertPoller(a)
	a.monitoring.polls = a.alertPoll.status
	a.hostContext = NewHostContextService(deps.Store, creds)
	a.status = NewStatusService(deps.Store, vault, db, dir, sessions, queue, a.ingestReady, opt)
	return a
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/meta", a.meta)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.authed(a.logout))
	mux.HandleFunc("GET /api/auth/me", a.authed(a.me))
	mux.HandleFunc("PUT /api/auth/me/preferences", a.authed(a.preferences))
	mux.HandleFunc("PUT /api/auth/me/profile", a.authed(a.updateProfile))
	mux.HandleFunc("PUT /api/auth/me/password", a.authed(a.changePassword))
	mux.HandleFunc("PUT /api/auth/me/avatar", a.authed(a.uploadAvatar))
	mux.HandleFunc("DELETE /api/auth/me/avatar", a.authed(a.deleteAvatar))
	mux.HandleFunc("GET /api/users/{id}/avatar", a.authed(a.avatar))
	mux.HandleFunc("PUT /api/settings", a.authed(a.can("status:defaults", a.updateSettings)))
	mux.HandleFunc("GET /api/system", a.authed(a.can("status:view", a.system)))
	for _, register := range []func(*http.ServeMux){a.registerRefs, a.registerPostgres, a.registerOpenBao, a.registerDirectory, a.registerEntra, a.registerPolicy, a.registerUsers, a.registerRoles, a.registerTeams, a.registerServices, a.registerConnectors, a.registerNetBox, a.registerInventoryDB, a.registerMonitoring, a.registerCMDB, a.registerGroups, a.registerIncidents, a.registerPagerDuty, a.registerNotifications, a.registerMaintenance, a.registerWallboards, a.registerRules, a.registerGrafana, a.registerFlow, a.registerOnboarding, a.registerGuides, a.registerResponse, a.registerTelegram, a.registerHostContext, a.registerServiceAccounts, a.registerFlowScope} {
		register(mux)
	}
	a.registerRoutePreview(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, ErrNotFound) })
	mux.Handle("/", a.opt.Web)
	return httpx.Secure(mux)
}

// Run synchronizes NetBox, directory groups and monitoring hosts on their schedules, prepares the ingest and alert
// tables, processes received requests and runs the alert engine until ctx ends. Without PostgreSQL
// (tests) the intake answers 503. Run returns only after every worker it started has stopped, so
// a runtime switch of the database copies data nobody writes any more.
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { a.netbox.Run(ctx) })
	wg.Go(func() { a.inventory.Run(ctx) })
	wg.Go(func() { a.groups.Run(ctx) })
	wg.Go(func() { a.monitoring.Run(ctx) })
	wg.Go(func() { a.alertPoll.Run(ctx) })
	if a.queue == nil {
		return
	}
	for {
		err := ingest.EnsureSchema(ctx, a.deps.Backend.Pool())
		if err == nil {
			err = alert.EnsureSchema(ctx, a.deps.Backend.Pool())
		}
		if err == nil {
			err = response.EnsureSchema(ctx, a.deps.Backend.Pool())
		}
		if err == nil {
			break
		}
		slog.Error("ingest and alert tables are not ready", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
	if key, err := alert.LinkKey(ctx, a.deps.Backend.Pool()); err != nil {
		slog.Error("acknowledgement links are off: no signing key", "err", err)
	} else {
		a.notifier.SetLinks(notify.NewLinks(key))
		wg.Go(func() { a.tgBot.Run(ctx) })
	}
	a.ready.Store(true)
	wg.Go(func() { a.alerts.Run(ctx) })
	wg.Go(func() { alert.Watch(ctx, a.deps.Backend.Pool, a.feed.publish) })
	wg.Go(func() { a.pdGateway.Run(ctx) })
	wg.Go(func() { a.pdGateway.RunSync(ctx) })
	wg.Go(func() { a.notifier.Run(ctx) })
	wg.Go(func() { a.ruleEngine.Run(ctx) })
	wg.Go(func() { a.response.Run(ctx) })
	a.queue.Run(ctx, a.opt.Ingest.Workers, a.process)
}

func (a *App) ingestReady() bool { return a.queue != nil && a.ready.Load() }

// IngestReady reports whether the intake tables exist and the workers run.
func (a *App) IngestReady() bool { return a.ingestReady() }

type metaView struct {
	defaultsView
	Mode          string `json:"mode"`
	SetupRequired bool   `json:"setup_required"`
	Version       string `json:"version"`
	LDAPEnabled   bool   `json:"ldap_enabled"`
	EntraEnabled  bool   `json:"entra_enabled"`
}

func (a *App) meta(w http.ResponseWriter, r *http.Request) {
	s := a.settings.Get()
	httpx.JSON(w, http.StatusOK, metaView{defaultsView: defaultsOf(s), Mode: "ready", Version: a.opt.Version, LDAPEnabled: s.LDAP.Enabled, EntraEnabled: s.Entra.Enabled})
}

type noSecrets struct{}

func (noSecrets) Resolve(string) (string, error) { return "", credentials.ErrUnavailable }
