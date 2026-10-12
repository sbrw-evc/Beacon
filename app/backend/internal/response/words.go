package response

import "strings"

// words of the messages incident response writes (escalation messages, war room posts, Jira
// issues) in the default language of the installation.
var words = map[string]map[string]string{
	"ru": {
		"say.critical": "первый, критический", "say.error": "второй, высокий", "say.warning": "третий, средний", "say.low": "четвёртый, низкий", "say.info": "пятый, информационный",
		"say.none":         "нет",
		"say.team.none":    "не назначена",
		"say.ack.teams":    "Чтобы подтвердить инцидент, нажмите 1.",
		"say.ack.telegram": "Подтвердить инцидент можно кнопкой под сообщением.",
		"say.confirm":      "Инцидент подтверждён. Спасибо.",
		"say.test":         "Это проверка голосовых звонков Umbrella. Если вы слышите это сообщение, всё настроено.",
		"say.test.title":   "Проверка голосовых звонков",
		"sample.title":     "Рост ошибок HTTP 5xx на pay-01",
		"sample.service":   "Платежи",
		"sample.team":      "Payments SRE",
		"critical":         "P1 · критический", "error": "P2 · высокий", "warning": "P3 · средний", "low": "P4 · низкий", "info": "P5 · информационный",
		"extensive": "широкое", "significant": "значительное", "moderate": "умеренное", "minor": "незначительное",
		"open": "открыт", "acknowledged": "взят в работу", "resolved": "решён",
		"subj.step1":         "[Umbrella] {priority} · {id} · {title}",
		"subj.stepN":         "[Umbrella] Эскалация {level}: {priority} · {id} · {title}",
		"head.step1":         "Новый инцидент: требуется реакция",
		"head.stepN":         "Эскалация, уровень {level}: инцидент не решён",
		"priority":           "Приоритет",
		"priority.line":      "{priority} (влияние: {impact}; важность событий: {urgency})",
		"services":           "Бизнес-сервисы",
		"dep":                "{name} (зависит от {via})",
		"ci":                 "КЕ",
		"team":               "Команда",
		"opened":             "Открыт",
		"status":             "Статус",
		"status.ack":         "взят в работу ({who})",
		"room":               "Чат инцидента",
		"bridge":             "Звонок",
		"jira":               "Задача Jira",
		"postmortem":         "Постмортем",
		"umbrella":           "Открыть в Umbrella",
		"grafana":            "Grafana",
		"ack":                "Подтвердить",
		"room.topic":         "{priority} {id} · {title}",
		"room.purpose":       "Чат инцидента: здесь команда восстанавливает сервис, а затем находит причину и меры, чтобы это не повторилось.",
		"room.summary":       "Сводка",
		"room.impact":        "Влияние на бизнес",
		"room.reasons":       "Почему такой приоритет",
		"room.sources":       "Источники событий",
		"room.people":        "Кто подключён",
		"room.links":         "Ссылки",
		"room.plan":          "Порядок работы",
		"room.plan.1":        "Назначить ответственного и подтвердить инцидент в Umbrella.",
		"room.plan.2":        "Восстановить сервис: обходное решение важнее поиска причины.",
		"room.plan.3":        "Записывать здесь гипотезы, действия и время.",
		"room.plan.4":        "После решения — постмортем: причина, хронология, меры против повторения.",
		"upd.ack":            "Инцидент взят в работу: {who}.",
		"upd.resolved":       "Инцидент решён{who}. Чат остаётся для разбора.",
		"upd.reopened":       "Инцидент снова открыт.",
		"upd.raised":         "Приоритет повышен: {from} → {to}.",
		"upd.step":           "Эскалация, уровень {level}: подключены {people}.",
		"upd.postmortem":     "Создан постмортем {key}: {url}",
		"upd.task":           "Создана задача {key}: {url}",
		"upd.bridge":         "Звонок по инциденту: {url}",
		"reason.no_service":  "КЕ не входит ни в один бизнес-сервис — влияние «{impact}»",
		"reason.criticality": "Самый критичный затронутый сервис «{service}» ({criticality}) — влияние «{impact}»",
		"reason.red":         "Сигнал RED виден пользователям — влияние {from} → {to}",
		"reason.dependents":  "От затронутых сервисов зависят ещё {count} — влияние {from} → {to}",
		"reason.mass":        "Активных инцидентов по тем же сервисам: {count} — влияние {from} → {to}",
		"reason.matrix":      "Матрица: влияние «{impact}» × важность {urgency} = {priority}",
		"reason.never_lower": "Приоритет не ниже важности событий: {priority}",
		"crit.critical":      "критичный", "crit.high": "высокий", "crit.medium": "средний", "crit.low": "низкий",
		"task.summary":     "[{priority}] {id}: {title}",
		"task.goal":        "Восстановить работу сервиса и устранить инцидент.",
		"pm.summary":       "Постмортем {id}: {title}",
		"pm.goal":          "Разобрать инцидент без поиска виноватых: что произошло, почему, и что сделать, чтобы это не повторилось.",
		"pm.timeline":      "Хронология",
		"pm.cause":         "Корневая причина",
		"pm.cause.todo":    "Заполнить: техническая причина и условия, при которых она сработала.",
		"pm.detect":        "Обнаружение и реакция",
		"pm.detect.text":   "Открыт {opened}, взят в работу {acked}, решён {resolved}. Время до подтверждения: {tta}, время до решения: {ttr}.",
		"pm.actions":       "Меры против повторения",
		"pm.actions.todo":  "Заполнить: задачи с владельцами и сроками (мониторинг, автоматизация, исправления).",
		"pm.lessons":       "Что сработало и что нет",
		"comment.ack":      "Umbrella: инцидент {id} взят в работу ({who}).",
		"comment.resolved": "Umbrella: инцидент {id} решён{who}.",
		"comment.reopened": "Umbrella: инцидент {id} снова открыт.",
		"comment.raised":   "Umbrella: приоритет инцидента {id} повышен: {from} → {to}.",
		"none":             "—",
		"by":               " ({who})",
	},
	"en": {
		"say.critical": "one, critical", "say.error": "two, high", "say.warning": "three, medium", "say.low": "four, low", "say.info": "five, informational",
		"say.none":         "none",
		"say.team.none":    "not assigned",
		"say.ack.teams":    "To acknowledge the incident, press 1.",
		"say.ack.telegram": "You can acknowledge the incident with the button below this message.",
		"say.confirm":      "The incident is acknowledged. Thank you.",
		"say.test":         "This is a test of Umbrella voice calls. If you hear this message, everything is set up.",
		"say.test.title":   "Voice call test",
		"sample.title":     "HTTP 5xx errors growing on pay-01",
		"sample.service":   "Payments",
		"sample.team":      "Payments SRE",
		"critical":         "P1 · critical", "error": "P2 · high", "warning": "P3 · medium", "low": "P4 · low", "info": "P5 · informational",
		"extensive": "extensive", "significant": "significant", "moderate": "moderate", "minor": "minor",
		"open": "open", "acknowledged": "acknowledged", "resolved": "resolved",
		"subj.step1":         "[Umbrella] {priority} · {id} · {title}",
		"subj.stepN":         "[Umbrella] Escalation {level}: {priority} · {id} · {title}",
		"head.step1":         "New incident: response needed",
		"head.stepN":         "Escalation level {level}: the incident is not resolved",
		"priority":           "Priority",
		"priority.line":      "{priority} (impact: {impact}; event severity: {urgency})",
		"services":           "Business services",
		"dep":                "{name} (depends on {via})",
		"ci":                 "CI",
		"team":               "Team",
		"opened":             "Opened",
		"status":             "Status",
		"status.ack":         "acknowledged ({who})",
		"room":               "Incident chat",
		"bridge":             "Call",
		"jira":               "Jira task",
		"postmortem":         "Postmortem",
		"umbrella":           "Open in Umbrella",
		"grafana":            "Grafana",
		"ack":                "Acknowledge",
		"room.topic":         "{priority} {id} · {title}",
		"room.purpose":       "Incident chat: the team restores the service here, then finds the cause and how to keep it from happening again.",
		"room.summary":       "Summary",
		"room.impact":        "Business impact",
		"room.reasons":       "Why this priority",
		"room.sources":       "Event sources",
		"room.people":        "Who is in",
		"room.links":         "Links",
		"room.plan":          "How we work",
		"room.plan.1":        "Name an incident owner and acknowledge the incident in Umbrella.",
		"room.plan.2":        "Restore the service: a workaround comes before the root cause.",
		"room.plan.3":        "Write hypotheses, actions and times here.",
		"room.plan.4":        "After resolution, a postmortem: cause, timeline, measures against recurrence.",
		"upd.ack":            "The incident is acknowledged by {who}.",
		"upd.resolved":       "The incident is resolved{who}. The chat stays for the review.",
		"upd.reopened":       "The incident opened again.",
		"upd.raised":         "Priority raised: {from} → {to}.",
		"upd.step":           "Escalation level {level}: {people} called in.",
		"upd.postmortem":     "Postmortem {key} created: {url}",
		"upd.task":           "Task {key} created: {url}",
		"upd.bridge":         "Incident call: {url}",
		"reason.no_service":  "The CI belongs to no business service: impact «{impact}»",
		"reason.criticality": "The most critical affected service «{service}» ({criticality}): impact «{impact}»",
		"reason.red":         "A RED signal is what users see: impact {from} → {to}",
		"reason.dependents":  "{count} more services depend on the affected ones: impact {from} → {to}",
		"reason.mass":        "Active incidents on the same services: {count}: impact {from} → {to}",
		"reason.matrix":      "Matrix: impact «{impact}» × severity {urgency} = {priority}",
		"reason.never_lower": "The priority is not below the event severity: {priority}",
		"crit.critical":      "critical", "crit.high": "high", "crit.medium": "medium", "crit.low": "low",
		"task.summary":     "[{priority}] {id}: {title}",
		"task.goal":        "Restore the service and resolve the incident.",
		"pm.summary":       "Postmortem {id}: {title}",
		"pm.goal":          "A blameless review: what happened, why, and what to do so it does not happen again.",
		"pm.timeline":      "Timeline",
		"pm.cause":         "Root cause",
		"pm.cause.todo":    "To fill in: the technical cause and the conditions that triggered it.",
		"pm.detect":        "Detection and response",
		"pm.detect.text":   "Opened {opened}, acknowledged {acked}, resolved {resolved}. Time to acknowledge: {tta}, time to resolve: {ttr}.",
		"pm.actions":       "Measures against recurrence",
		"pm.actions.todo":  "To fill in: tasks with owners and due dates (monitoring, automation, fixes).",
		"pm.lessons":       "What went well and what did not",
		"comment.ack":      "Umbrella: incident {id} acknowledged ({who}).",
		"comment.resolved": "Umbrella: incident {id} resolved{who}.",
		"comment.reopened": "Umbrella: incident {id} opened again.",
		"comment.raised":   "Umbrella: priority of incident {id} raised: {from} → {to}.",
		"none":             "—",
		"by":               " ({who})",
	},
}

// tr is a word in a language (Russian by default) with its {placeholders} filled by pairs.
func tr(locale, key string, kv ...string) string {
	w := words["ru"]
	if locale == "en" {
		w = words["en"]
	}
	v, ok := w[key]
	if !ok {
		v = key
	}
	for i := 0; i+1 < len(kv); i += 2 {
		v = strings.ReplaceAll(v, "{"+kv[i]+"}", kv[i+1])
	}
	return v
}

// reasonText is a reason of the assessment in words.
func reasonText(locale string, r Reason) string {
	kv := []string{}
	for k, v := range r.Args {
		switch k {
		case "impact", "from", "to":
			v = tr(locale, v)
		case "urgency", "priority":
			v = tr(locale, v)
		case "criticality":
			v = tr(locale, "crit."+v)
		}
		kv = append(kv, k, v)
	}
	return tr(locale, "reason."+r.Code, kv...)
}
