package tgbot

import "strings"

var words = map[string]map[string]string{
	"ru": {
		"welcome":            "👋 Это бот Umbrella. Он присылает инциденты и позволяет работать с ними прямо из Telegram.",
		"help":               "<b>Как пользоваться</b>\nПривяжите аккаунт: в Umbrella откройте профиль и нажмите «Привязать Telegram».\n\nКнопки под оповещением подтверждают и решают инцидент. Ответ на оповещение добавляет комментарий.\n\n/incidents — активные инциденты\n/ack ID — подтвердить\n/resolve ID — решить\n/comment ID текст — комментарий\n/me — чей аккаунт привязан\n/unlink — отвязать аккаунт",
		"linked":             "✅ Аккаунт привязан: {who}. Оповещения будут приходить сюда.",
		"unlinked":           "Аккаунт отвязан. Оповещения в этот чат больше не придут.",
		"me":                 "Привязан аккаунт {who} ({login}).",
		"active":             "Активные инциденты",
		"none":               "Активных инцидентов нет 🎉",
		"list.hint":          "Подтвердить: /ack ID · решить: /resolve ID",
		"done.ack":           "Подтверждено: инцидент взят в работу",
		"done.resolve":       "Инцидент решён",
		"done.comment":       "Комментарий добавлен",
		"err.not_open":       "Инцидент уже взят в работу",
		"err.not_active":     "Инцидент уже решён",
		"err.not_found":      "Инцидент не найден или недоступен вам",
		"err.failed":         "Не получилось, попробуйте позже",
		"err.unlinked":       "Аккаунт Telegram не привязан к Umbrella. Откройте профиль в Umbrella и нажмите «Привязать Telegram».",
		"err.forbidden":      "У вашей роли нет права подтверждать инциденты",
		"err.forbidden_view": "У вашей роли нет права просматривать инциденты",
		"err.link":           "Ссылка недействительна или устарела. Получите новую в профиле Umbrella.",
		"err.private":        "Привязывать аккаунт нужно в личном чате с ботом.",
		"err.no_id":          "Укажите номер инцидента, например /ack INC-12, или ответьте командой на оповещение.",
		"err.no_comment":     "Укажите номер и текст: /comment INC-12 текст, или ответьте на оповещение.",
		"critical":           "P1",
		"error":              "P2",
		"warning":            "P3",
		"low":                "P4",
		"info":               "P5",
	},
	"en": {
		"welcome":            "👋 This is the Umbrella bot. It sends incidents and lets you work on them right from Telegram.",
		"help":               "<b>How to use it</b>\nLink your account: in Umbrella open your profile and press «Link Telegram».\n\nThe buttons under a notification acknowledge and resolve the incident. A reply to a notification adds a comment.\n\n/incidents — active incidents\n/ack ID — acknowledge\n/resolve ID — resolve\n/comment ID text — comment\n/me — which account is linked\n/unlink — unlink the account",
		"linked":             "✅ Account linked: {who}. Notifications will come here.",
		"unlinked":           "Account unlinked. Notifications no longer come to this chat.",
		"me":                 "Linked account: {who} ({login}).",
		"active":             "Active incidents",
		"none":               "No active incidents 🎉",
		"list.hint":          "Acknowledge: /ack ID · resolve: /resolve ID",
		"done.ack":           "Acknowledged: the incident is taken",
		"done.resolve":       "The incident is resolved",
		"done.comment":       "Comment added",
		"err.not_open":       "The incident is already taken",
		"err.not_active":     "The incident is already resolved",
		"err.not_found":      "The incident is not found or not visible to you",
		"err.failed":         "That did not work, try again later",
		"err.unlinked":       "This Telegram account is not linked to Umbrella. Open your profile in Umbrella and press «Link Telegram».",
		"err.forbidden":      "Your role may not acknowledge incidents",
		"err.forbidden_view": "Your role may not view incidents",
		"err.link":           "The link is not valid or has expired. Get a new one in your Umbrella profile.",
		"err.private":        "Link your account in a private chat with the bot.",
		"err.no_id":          "Give the incident number, for example /ack INC-12, or reply to a notification with the command.",
		"err.no_comment":     "Give the number and the text: /comment INC-12 text, or reply to a notification.",
		"critical":           "P1",
		"error":              "P2",
		"warning":            "P3",
		"low":                "P4",
		"info":               "P5",
	},
}

// wordsFor is the language of the Telegram user when the bot speaks it, else the default
// language of Umbrella (Russian unless English).
func wordsFor(tgLang, def string) map[string]string {
	switch {
	case strings.HasPrefix(tgLang, "ru"):
		return words["ru"]
	case strings.HasPrefix(tgLang, "en"):
		return words["en"]
	case def == "en":
		return words["en"]
	}
	return words["ru"]
}

// commandMenu is the command menu of a language ("" is the default one, Russian).
func commandMenu(lang string) [][2]string {
	if lang == "en" {
		return [][2]string{{"incidents", "Active incidents"}, {"ack", "Acknowledge: /ack ID"}, {"resolve", "Resolve: /resolve ID"},
			{"comment", "Comment: /comment ID text"}, {"me", "Linked account"}, {"unlink", "Unlink the account"}, {"help", "Help"}}
	}
	return [][2]string{{"incidents", "Активные инциденты"}, {"ack", "Подтвердить: /ack ID"}, {"resolve", "Решить: /resolve ID"},
		{"comment", "Комментарий: /comment ID текст"}, {"me", "Привязанный аккаунт"}, {"unlink", "Отвязать аккаунт"}, {"help", "Справка"}}
}
