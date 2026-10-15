package access

import (
	"slices"
	"strings"
)

type Text struct {
	EN string `json:"en"`
	RU string `json:"ru"`
}

type Feature struct {
	ID    string `json:"id"`
	Title Text   `json:"title"`
}

type Page struct {
	ID       string    `json:"id"`
	Group    string    `json:"group"`
	Title    Text      `json:"title"`
	Features []Feature `json:"features"`
}

type Group struct {
	ID    string `json:"id"`
	Title Text   `json:"title"`
}

const (
	View = "view"

	GroupOverview = "overview"
	GroupAuto     = "automation"
	GroupOrg      = "org"
	GroupSettings = "settings"
)

var (
	view    = Feature{View, Text{"View the page", "Просмотр страницы"}}
	edit    = Feature{"edit", Text{"Create, change and delete", "Создание, изменение и удаление"}}
	test    = Feature{"test", Text{"Check the connection", "Проверка подключения"}}
	migrate = Feature{"migrate", Text{"Move data to another installation", "Перенос данных в другую установку"}}
)

var groups = []Group{
	{GroupOverview, Text{"Overview", "Обзор"}},
	{GroupAuto, Text{"Automation", "Автоматизация"}},
	{GroupOrg, Text{"Organization", "Организация"}},
	{GroupSettings, Text{"Settings", "Настройки"}},
}

var pages = []Page{
	{"incidents", GroupOverview, Text{"Incidents", "Инциденты"}, []Feature{
		view,
		{"ack", Text{"Acknowledge, resolve and comment incidents", "Подтверждение, решение и комментарии инцидентов"}},
	}},
	{"cmdb", GroupOverview, Text{"CMDB map", "Карта CMDB"}, []Feature{
		view,
		{"propose", Text{"Submit dependency proposals (Umbrella Flow)", "Отправка предложений зависимостей (Umbrella Flow)"}},
	}},
	{"cis", GroupOverview, Text{"Configuration items", "Конфигурационные единицы"}, []Feature{
		view,
		{"edit", Text{"Create, change and delete configuration items, also in NetBox", "Создание, изменение и удаление КЕ, в том числе в NetBox"}},
	}},
	{"services", GroupOverview, Text{"Business services", "Бизнес-сервисы"}, []Feature{
		view,
		{"edit", Text{"Create, change and delete services, bind configuration items, link services to NetBox", "Создание, изменение и удаление сервисов, привязка КЕ, связь с NetBox"}},
	}},
	{"maintenance", GroupOverview, Text{"Maintenance windows", "Сервисные окна"}, []Feature{
		view,
		{"edit", Text{"Plan, change, finish and delete maintenance windows", "Планирование, изменение, завершение и удаление сервисных окон"}},
	}},
	{"wallboards", GroupOverview, Text{"TV wallboards", "ТВ-панели"}, []Feature{
		view,
		{"edit", Text{"Create, change and delete TV wallboards and their allowed networks", "Создание, изменение и удаление ТВ-панелей и разрешённых сетей"}},
		{"proxies", Text{"Change trusted proxies (they also decide the address for sign-in limits and the audit)", "Изменение доверенных прокси (от них зависит и адрес для ограничений входа и аудита)"}},
	}},
	{"connectors", GroupAuto, Text{"Connectors", "Коннекторы"}, []Feature{
		view,
		{"edit", Text{"Create and change drafts, samples and test runs", "Создание и изменение черновиков, образцов и тестовых прогонов"}},
		{"publish", Text{"Publish and stop connectors", "Публикация и остановка коннекторов"}},
		{"payload", Text{"See request bodies, samples and failed records", "Просмотр тел запросов, образцов и ошибочных записей"}},
	}},
	{"rules", GroupAuto, Text{"RED/USE rules", "Правила RED/USE"}, []Feature{
		view,
		{"edit", Text{"Create, change and delete rules and metric sources", "Создание, изменение и удаление правил и источников метрик"}},
	}},
	{"netbox", GroupAuto, Text{"NetBox", "NetBox"}, []Feature{
		view,
		test,
		{"edit", Text{"Change the connection and synchronization settings", "Изменение подключения и настроек синхронизации"}},
		{"sync", Text{"Run synchronization", "Запуск синхронизации"}},
	}},
	{"inventorydb", GroupAuto, Text{"Inventory DB", "Inventory DB"}, []Feature{
		view,
		test,
		{"edit", Text{"Change the connection, synchronization and alert state settings", "Изменение подключения, синхронизации и отправки статусов тревог"}},
		{"sync", Text{"Run synchronization", "Запуск синхронизации"}},
	}},
	{"monitoring", GroupAuto, Text{"Monitoring systems", "Системы мониторинга"}, []Feature{
		view,
		test,
		{"edit", Text{"Add, change and delete Zabbix, Prometheus, Grafana and Graylog connections", "Добавление, изменение и удаление подключений Zabbix, Prometheus, Grafana и Graylog"}},
		{"sync", Text{"Read host lists and poll alerts", "Загрузка списков хостов и опрос алертов"}},
		{"link", Text{"Link hosts to configuration items and create items from hosts", "Привязка хостов к КЕ и создание КЕ из хостов"}},
	}},
	{"response", GroupAuto, Text{"Incident response", "Реагирование на инциденты"}, []Feature{
		view,
		{"test", Text{"Check the Jira, Microsoft Teams and Zoom connections", "Проверка подключений Jira, Microsoft Teams и Zoom"}},
		{"edit", Text{"Change impact rules, priority policies, escalations and integrations", "Изменение правил влияния, политик приоритетов, эскалаций и интеграций"}},
	}},
	{"integrations.flow", GroupAuto, Text{"Umbrella Flow integration", "Интеграция с Umbrella Flow"}, []Feature{
		{View, Text{"No page of its own: given together with the functions below", "Своей страницы нет: выдаётся вместе с функциями ниже"}},
		{"read", Text{"Read the visibility scopes of users for Umbrella Flow", "Чтение областей видимости пользователей для Umbrella Flow"}},
	}},
	{"credentials", GroupAuto, Text{"Credentials", "Учётные данные"}, []Feature{
		view,
		{"edit", Text{"Create, replace and delete credentials", "Создание, замена и удаление учётных данных"}},
	}},
	{"users", GroupOrg, Text{"Users", "Пользователи"}, []Feature{
		view,
		{"edit", Text{"Change the teams and the incident scope of users (people and roles are managed in SPA)", "Изменение команд и области инцидентов пользователей (люди и роли управляются в SPA)"}},
		{"delete", Text{"Delete the records of people who no longer have access in SPA", "Удаление записей людей, у которых больше нет доступа в SPA"}},
	}},
	{"teams", GroupOrg, Text{"Teams", "Команды"}, []Feature{view, edit}},
	{"roles", GroupOrg, Text{"Roles", "Роли"}, []Feature{view, {"edit", Text{"Create, change and delete roles (they are published to SPA)", "Создание, изменение и удаление ролей (публикуются в SPA)"}}}},
	{"settings.alerting", GroupSettings, Text{"Alerting", "Оповещения"}, []Feature{
		view,
		{"test", Text{"Check connections and send test messages", "Проверка подключений и тестовые сообщения"}},
		{"edit", Text{"Change PagerDuty, backup notification and Grafana", "Изменение PagerDuty, резервного оповещения и Grafana"}},
	}},
	{"status", GroupSettings, Text{"System status", "Состояние системы"}, []Feature{view, {"defaults", Text{"Change default theme, language and time zone", "Изменение темы, языка и часового пояса по умолчанию"}}}},
	{"settings.ldap", GroupSettings, Text{"Active Directory for the CMDB", "Active Directory для CMDB"}, []Feature{view, test, {"edit", Text{"Change the connection", "Изменение подключения"}}}},
	{"settings.postgres", GroupSettings, Text{"PostgreSQL", "PostgreSQL"}, []Feature{view, test, migrate}},
	{"settings.openbao", GroupSettings, Text{"OpenBao", "OpenBao"}, []Feature{view, test, migrate}},
	{"settings.spa", GroupSettings, Text{"SPA connection", "Подключение к SPA"}, []Feature{
		view,
		{"sync", Text{"Synchronize people and publish roles now", "Синхронизация людей и публикация ролей вручную"}},
	}},
	{"serviceaccounts", GroupSettings, Text{"Service accounts", "Сервисные учётные записи"}, []Feature{
		view,
		{"edit", Text{"Create, change and delete service accounts, issue and revoke API tokens", "Создание, изменение и удаление сервисных учётных записей, выпуск и отзыв API-токенов"}},
	}},
}

func Groups() []Group { return slices.Clone(groups) }

func Pages() []Page { return slices.Clone(pages) }

func Perm(page, feature string) string { return page + ":" + feature }

// all and valid are computed once from pages: the catalog never changes at run time.
var all, valid = func() ([]string, map[string]bool) {
	var out []string
	set := map[string]bool{}
	for _, p := range pages {
		for _, f := range p.Features {
			out = append(out, Perm(p.ID, f.ID))
			set[Perm(p.ID, f.ID)] = true
		}
	}
	return out, set
}()

func All() []string { return slices.Clone(all) }

func Valid(perm string) bool { return valid[perm] }

func Normalize(perms []string) []string {
	set := map[string]bool{}
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if !Valid(p) {
			continue
		}
		set[p] = true
		page, _, _ := strings.Cut(p, ":")
		set[Perm(page, View)] = true
	}
	out := make([]string, 0, len(set))
	for _, p := range all {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

type Set map[string]bool

func NewSet(perms []string) Set {
	s := Set{}
	for _, p := range perms {
		s[p] = true
	}
	return s
}

func (s Set) Has(perm string) bool { return s[perm] }

func (s Set) List() []string {
	out := []string{}
	for _, p := range all {
		if s[p] {
			out = append(out, p)
		}
	}
	return out
}
