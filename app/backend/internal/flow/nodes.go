package flow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Webhook is the compiled configuration of trigger.webhook, applied by the intake before a
// request is accepted.
type Webhook struct {
	Credential string
	Anonymous  bool
	HMACHeader string
	HMACPrefix string
	Networks   []netip.Prefix
	MaxBody    int64
	Rate       float64
	AcceptIf   *Expr
}

func (w *Webhook) Allowed(ip string) bool {
	if len(w.Networks) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range w.Networks {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Ack is the compiled ack.response: what the source gets back once its request is stored.
type Ack struct {
	Status      int
	Body        string
	ContentType string
}

const (
	CredBearer = "bearer"
	CredBasic  = "basic"
	CredHeader = "header"
	CredHMAC   = "hmac"
)

var CredentialTypes = []string{CredBearer, CredBasic, CredHeader, CredHMAC}

// severityOptions are the levels of model.Severities, most severe first, shown with their priority.
var severityOptions = func() []Option {
	out := make([]Option, 0, len(model.Severities))
	for _, s := range model.Severities {
		out = append(out, Option{s.Name, Text{s.Priority + " · " + s.Title.En, s.Priority + " · " + s.Title.Ru}})
	}
	return out
}()

func init() {
	register(&NodeType{
		Type: "trigger.webhook", Version: 1, Category: CategoryTrigger, Singleton: true,
		Title: Text{"Webhook", "Входящий webhook"},
		Description: Text{
			"The source sends events to the connector address. Umbrella answers 202 once the request is stored and processes it right after.",
			"Источник отправляет события на адрес коннектора. Umbrella отвечает 202, как только запрос сохранён, и сразу обрабатывает его.",
		},
		Outputs: []string{OutMain},
		Params: []Param{
			{Key: "credential", Kind: KindCredential, CredentialTypes: CredentialTypes,
				Title: Text{"Authentication", "Аутентификация"},
				Help: Text{"Token, Basic, a header or an HMAC signature of the body. The secret is kept in OpenBao.",
					"Токен, Basic, заголовок или HMAC-подпись тела. Секрет хранится в OpenBao."}},
			{Key: "anonymous", Kind: KindBool, Default: false,
				Title: Text{"Accept requests without authentication", "Принимать запросы без аутентификации"},
				Help:  Text{"Only for closed networks: limit the allowed networks then.", "Только для закрытых сетей: тогда ограничьте разрешённые сети."}},
			{Key: "hmac_header", Kind: KindString, Default: "X-Signature",
				Title: Text{"Signature header (HMAC)", "Заголовок подписи (HMAC)"}},
			{Key: "hmac_prefix", Kind: KindString, Default: "sha256=",
				Title: Text{"Signature prefix (HMAC)", "Префикс подписи (HMAC)"},
				Help:  Text{"The header holds the prefix and the hex HMAC-SHA256 of the body.", "Заголовок содержит префикс и HMAC-SHA256 тела в hex."}},
			{Key: "allowed_networks", Kind: KindList, Placeholder: "10.0.0.0/8",
				Title: Text{"Allowed networks", "Разрешённые сети"},
				Help:  Text{"CIDR, one per line. Empty: any address.", "CIDR, по одной в строке. Пусто: любой адрес."}},
			{Key: "max_body_kb", Kind: KindNumber, Default: 1024.0, Min: ptr(1), Max: ptr(16384),
				Title: Text{"Body limit, KB", "Лимит тела, КБ"}},
			{Key: "rate_limit", Kind: KindNumber, Default: 0.0, Min: ptr(0), Max: ptr(100000),
				Title: Text{"Requests per second", "Запросов в секунду"},
				Help:  Text{"Above it the source gets 429 with Retry-After. 0: no limit.", "Сверх лимита источник получает 429 с Retry-After. 0: без лимита."}},
			{Key: "accept_if", Kind: KindCEL, Placeholder: `request.headers["x-env"] == "prod"`,
				Title: Text{"Accept only if", "Принимать, только если"},
				Help:  Text{"CEL condition over request. Other requests are answered 202 and ignored.", "Условие CEL над request. Остальные запросы получают 202 и не обрабатываются."}},
		},
		compile: func(c *compiler, n Node) Step {
			w := &Webhook{
				Credential: c.credentialRef("credential"),
				Anonymous:  c.boolean("anonymous"),
				HMACHeader: c.str("hmac_header"),
				HMACPrefix: c.text("hmac_prefix"),
				MaxBody:    int64(c.num("max_body_kb")) * 1024,
				Rate:       c.num("rate_limit"),
				AcceptIf:   c.condition("accept_if"),
			}
			if w.Credential == "" && !w.Anonymous {
				c.fail("credential", "auth_required", "choose a credential or allow requests without authentication")
			}
			for _, s := range c.list("allowed_networks") {
				p, err := netip.ParsePrefix(s)
				if err != nil {
					a, aerr := netip.ParseAddr(s)
					if aerr != nil {
						c.fail("allowed_networks", "cidr", "%q is not a network (CIDR) or address", s)
						continue
					}
					p = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
				}
				w.Networks = append(w.Networks, p.Masked())
			}
			c.webhook = w
			return func(x *Exec, r Record, emit func(string, Record)) error {
				emit(OutMain, r)
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "parse.json", Version: 1, Category: CategoryParse, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Parse JSON", "Разбор JSON"},
		Description: Text{
			"Parses the body as JSON. A path to an array splits a batch into separate records.",
			"Разбирает тело как JSON. Путь к массиву разбивает пачку на отдельные записи.",
		},
		Params: []Param{
			{Key: "items", Kind: KindPath, Placeholder: "alerts",
				Title: Text{"Path to the array of items", "Путь к массиву записей"},
				Help:  Text{"Empty: the whole body is one record, or a record per element if it is an array.", "Пусто: всё тело — одна запись или по записи на элемент, если это массив."}},
			{Key: "root_as", Kind: KindString, Placeholder: "root",
				Title: Text{"Keep the whole body in field", "Сохранить всё тело в поле"},
				Help:  Text{"Lets later nodes read common fields of a batch, such as commonLabels.", "Позволяет следующим узлам читать общие поля пачки, например commonLabels."}},
		},
		compile: func(c *compiler, n Node) Step {
			items := c.path("items")
			rootAs := c.str("root_as")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				var body any
				if strings.TrimSpace(r.Raw) != "" {
					dec := json.NewDecoder(strings.NewReader(r.Raw))
					if err := dec.Decode(&body); err != nil {
						return fmt.Errorf("the body is not valid JSON: %v", err)
					}
				} else {
					body = r.Data
				}
				v := body
				if items != nil {
					got, ok := items.Get(body)
					if !ok {
						return fmt.Errorf("the body has no %s", items)
					}
					v = got
				}
				list, isList := v.([]any)
				if !isList {
					list = []any{v}
				}
				for i, it := range list {
					rec := Record{Data: map[string]any{}, Lineage: Lineage{Request: r.Lineage.Request, Item: i}}
					if m, ok := deepCopy(it).(map[string]any); ok {
						rec.Data = m
					} else {
						rec.Data["value"] = deepCopy(it)
					}
					if rootAs != "" {
						// Shared by the records of one batch; nodes copy a record before changing it.
						rec.Data[rootAs] = body
					}
					raw, _ := json.Marshal(it)
					rec.Raw = string(raw)
					emit(OutMain, rec)
				}
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "filter", Version: 1, Category: CategoryRoute, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Filter", "Фильтр"},
		Description: Text{
			"Passes on only the records that match the condition. The rest are counted as filtered, not lost silently.",
			"Пропускает дальше только записи, подходящие под условие. Остальные учитываются как отфильтрованные, а не теряются молча.",
		},
		Params: []Param{
			{Key: "condition", Kind: KindCEL, Required: true, Placeholder: `event.status != "test"`,
				Title: Text{"Pass if", "Пропускать, если"},
				Help:  Text{"CEL condition over event and request.", "Условие CEL над event и request."}},
		},
		compile: func(c *compiler, n Node) Step {
			cond := c.condition("condition")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				ok, err := cond.Bool(x.ctx, x.scope(r))
				if err != nil {
					return err
				}
				if ok {
					emit(OutMain, r)
				} else {
					x.res.Filtered++
				}
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "route.switch", Version: 1, Category: CategoryRoute, Inputs: 1, DynamicOutputs: true, CanFail: true,
		Title: Text{"Switch", "Ветвление"},
		Description: Text{
			"Sends each record to the output of the first matching rule, or to “else”.",
			"Отправляет запись на выход первого подходящего правила или на «иначе».",
		},
		Params: []Param{
			{Key: "rules", Kind: KindTable, Required: true,
				Title: Text{"Rules", "Правила"},
				Help:  Text{"Output names: lowercase Latin letters, digits and _.", "Имена выходов: строчные латинские буквы, цифры и _."},
				Columns: []Param{
					{Key: "output", Kind: KindString, Required: true, Title: Text{"Output", "Выход"}, Placeholder: "kubernetes"},
					{Key: "condition", Kind: KindCEL, Required: true, Title: Text{"Condition", "Условие"}, Placeholder: `has(event.labels.namespace)`},
				}},
			{Key: "all_matches", Kind: KindBool, Default: false,
				Title: Text{"Send to every matching output", "Отправлять на все подходящие выходы"}},
		},
		outputs: func(n Node) []string {
			out := []string{}
			seen := map[string]bool{OutElse: true, OutError: true}
			if rows, ok := n.Params["rules"].([]any); ok {
				for _, r := range rows {
					if m, ok := r.(map[string]any); ok {
						if name, _ := m["output"].(string); outputName.MatchString(name) && !seen[name] {
							seen[name] = true
							out = append(out, name)
						}
					}
				}
			}
			return append(out, OutElse)
		},
		compile: func(c *compiler, n Node) Step {
			type rule struct {
				out  string
				cond *Expr
			}
			var rules []rule
			names := map[string]bool{}
			for i, row := range c.table("rules") {
				name := strings.TrimSpace(row["output"])
				switch {
				case !outputName.MatchString(name) || name == OutElse || name == OutError:
					c.fail("rules", "output", "row %d: output name %q is not allowed", i+1, name)
					continue
				case names[name]:
					c.fail("rules", "output", "row %d: output %q is used twice", i+1, name)
					continue
				}
				names[name] = true
				x, err := CompileCondition(row["condition"])
				if err != nil {
					c.fail("rules", "cel", "row %d: %v", i+1, err)
					continue
				}
				rules = append(rules, rule{name, x})
			}
			if len(rules) == 0 && !hasIssue(c.issues, "rules") {
				c.fail("rules", "required", "add at least one rule")
			}
			all := c.boolean("all_matches")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				matched := false
				for _, rl := range rules {
					ok, err := rl.cond.Bool(x.ctx, x.scope(r))
					if err != nil {
						return fmt.Errorf("rule %s: %w", rl.out, err)
					}
					if ok {
						emit(rl.out, r)
						matched = true
						if !all {
							return nil
						}
					}
				}
				if !matched {
					emit(OutElse, r)
				}
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "map.severity", Version: 1, Category: CategoryTransform, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Severity table", "Таблица severity"},
		Description: Text{
			"Translates the source's value into a priority by a table: P1 critical, P2 error, P3 warning, P4 low or P5 info.",
			"Переводит значение источника в приоритет по таблице: P1 critical, P2 error, P3 warning, P4 low или P5 info.",
		},
		Params: []Param{
			{Key: "source", Kind: KindTemplate, Required: true, Placeholder: "${labels.severity}",
				Title: Text{"Source value", "Значение источника"}},
			{Key: "mapping", Kind: KindTable,
				Title: Text{"Table", "Таблица"},
				Help:  Text{"Values are compared ignoring case. * matches anything.", "Значения сравниваются без учёта регистра. * — любое значение."},
				Columns: []Param{
					{Key: "from", Kind: KindString, Required: true, Title: Text{"Source value", "Значение источника"}, Placeholder: "5"},
					{Key: "to", Kind: KindSelect, Required: true, Title: Text{"Severity", "Severity"}, Options: severityOptions},
				}},
			{Key: "default", Kind: KindSelect, Options: severityOptions,
				Title: Text{"When nothing matches", "Если ничего не подошло"},
				Help:  Text{"Empty: known words (high, disaster, p1…) are recognized, otherwise the record fails.", "Пусто: распознаются известные слова (high, disaster, p1…), иначе запись — ошибка."}},
			{Key: "target", Kind: KindPath, Default: "severity",
				Title: Text{"Write to field", "Записать в поле"}},
		},
		compile: func(c *compiler, n Node) Step {
			src := c.template("source")
			table := map[string]string{}
			wildcard := ""
			for i, row := range c.table("mapping") {
				to := row["to"]
				if _, ok := NormalizeSeverity(to); !ok {
					c.fail("mapping", "option", "row %d: %q is not a severity", i+1, to)
				}
				from := strings.ToLower(strings.TrimSpace(row["from"]))
				if from == "*" {
					wildcard = to
				} else {
					table[from] = to
				}
			}
			def := c.choice("default")
			target := c.pathOr("target", "severity")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				v, err := src.String(r.Data)
				if err != nil {
					return err
				}
				sev, ok := table[strings.ToLower(strings.TrimSpace(v))]
				if !ok {
					sev, ok = NormalizeSeverity(v)
				}
				switch {
				case ok:
				case wildcard != "":
					sev = wildcard
				case def != "":
					sev = def
				default:
					return fmt.Errorf("no severity for value %q", v)
				}
				r = r.clone()
				if err := target.Set(r.Data, sev); err != nil {
					return err
				}
				emit(OutMain, r)
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "enrich.labels", Version: 1, Category: CategoryTransform, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Labels", "Метки"},
		Description: Text{
			"Adds fixed or computed labels. Values may use ${…}.",
			"Добавляет постоянные или вычисленные метки. В значениях можно использовать ${…}.",
		},
		Params: []Param{
			{Key: "labels", Kind: KindTable, Required: true,
				Title: Text{"Labels", "Метки"},
				Columns: []Param{
					{Key: "key", Kind: KindString, Required: true, Title: Text{"Name", "Имя"}, Placeholder: "env"},
					{Key: "value", Kind: KindTemplate, Title: Text{"Value", "Значение"}, Placeholder: "${labels.environment|prod}"},
				}},
			{Key: "target", Kind: KindPath, Default: "labels",
				Title: Text{"Labels field", "Поле меток"}},
		},
		compile: func(c *compiler, n Node) Step {
			type label struct {
				key string
				val *Template
			}
			var labels []label
			for i, row := range c.table("labels") {
				t, err := CompileTemplate(row["value"])
				if err != nil {
					c.fail("labels", "template", "row %d: %v", i+1, err)
					continue
				}
				labels = append(labels, label{strings.TrimSpace(row["key"]), t})
			}
			target := c.pathOr("target", "labels")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				r = r.clone()
				cur, _ := target.Get(r.Data)
				m, ok := cur.(map[string]any)
				if !ok {
					m = map[string]any{}
				}
				for _, l := range labels {
					v, err := l.val.String(r.Data)
					if err != nil {
						return err
					}
					m[l.key] = v
				}
				if err := target.Set(r.Data, m); err != nil {
					return err
				}
				emit(OutMain, r)
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "map.event", Version: 1, Category: CategoryTransform, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Event mapping", "Сопоставление события"},
		Description: Text{
			"Builds the Umbrella event from the source fields: title, configuration item, signal, severity, status.",
			"Строит событие Umbrella из полей источника: заголовок, КЕ, сигнал, severity, статус.",
		},
		Params: []Param{
			{Key: "title", Kind: KindTemplate, Required: true, Placeholder: "${annotations.summary|$labels.alertname}", Title: Text{"Title", "Заголовок"}},
			{Key: "ci", Kind: KindTemplate, Required: true, Placeholder: "${labels.instance}",
				Title: Text{"Configuration item", "КЕ"},
				Help:  Text{"Host name, address or ID of the object the event is about.", "Имя хоста, адрес или ID объекта, к которому относится событие."}},
			{Key: "signal", Kind: KindTemplate, Placeholder: "${labels.alertname}", Title: Text{"Signal", "Сигнал"}},
			{Key: "method", Kind: KindSelect, Default: MethodOther, Title: Text{"Method", "Метод"},
				Options: []Option{{MethodRED, Text{"RED", "RED"}}, {MethodUSE, Text{"USE", "USE"}}, {MethodOther, Text{"Other", "Другое"}}}},
			{Key: "severity", Kind: KindTemplate, Default: "${severity}", Title: Text{"Severity", "Severity"},
				Help: Text{"critical (P1), error (P2), warning (P3), low (P4) or info (P5); common synonyms and P1…P5 are recognized.", "critical (P1), error (P2), warning (P3), low (P4) или info (P5); распространённые синонимы и P1…P5 распознаются."}},
			{Key: "status", Kind: KindTemplate, Default: "${status}", Title: Text{"Status", "Статус"},
				Help: Text{"resolved, ok, closed, recovery… mean resolved; empty means firing.", "resolved, ok, closed, recovery… — resolved; пусто — firing."}},
			{Key: "external_id", Kind: KindTemplate, Placeholder: "${fingerprint}", Title: Text{"ID in the source", "ID в источнике"}},
			{Key: "value", Kind: KindTemplate, Placeholder: "${value}", Title: Text{"Value", "Значение"}},
			{Key: "description", Kind: KindTemplate, Placeholder: "${text|$annotations.description}",
				Title: Text{"Description (full text)", "Описание (полный текст)"},
				Help: Text{"Shown in the incident collapsed to its first lines and expanded on demand; up to 64 KB. A multi-line or long title or value is cut to one line and its full text is added here.",
					"Показывается в инциденте свёрнутым до первых строк и разворачивается по запросу; до 64 КБ. Многострочный или длинный заголовок или значение обрезаются до одной строки, а их полный текст добавляется сюда."}},
			{Key: "fields", Kind: KindTable,
				Title: Text{"Incident fields", "Поля инцидента"},
				Help:  Text{"Named values shown in the incident in this order; empty values are left out. Links are clickable.", "Именованные значения, показываемые в инциденте в этом порядке; пустые не показываются. Ссылки кликабельны."},
				Columns: []Param{
					{Key: "name", Kind: KindString, Required: true, Title: Text{"Name", "Название"}, Placeholder: "Дашборд"},
					{Key: "value", Kind: KindTemplate, Title: Text{"Value", "Значение"}, Placeholder: "${dashboardURL}"},
				}},
			{Key: "dedup_key", Kind: KindTemplate,
				Title: Text{"Deduplication key", "Ключ дедупликации"},
				Help: Text{"Repeated deliveries with the same key update one event, and resolved replaces firing. Do not put the status in the key. Default: ID in the source, otherwise configuration item and signal.",
					"Повторные доставки с тем же ключом обновляют одно событие, resolved заменяет firing. Не включайте статус в ключ. По умолчанию: ID в источнике, иначе КЕ и сигнал."}},
		},
		compile: func(c *compiler, n Node) Step {
			title, ci, signal := c.template("title"), c.template("ci"), c.template("signal")
			method := c.choice("method")
			sev, status := c.template("severity"), c.template("status")
			ext, value, key := c.template("external_id"), c.template("value"), c.template("dedup_key")
			desc := c.template("description")
			type field struct {
				name string
				val  *Template
			}
			var fields []field
			for i, row := range c.table("fields") {
				t, err := CompileTemplate(row["value"])
				if err != nil {
					c.fail("fields", "template", "row %d: %v", i+1, err)
					continue
				}
				fields = append(fields, field{strings.TrimSpace(row["name"]), t})
			}
			return func(x *Exec, r Record, emit func(string, Record)) error {
				var errs []error
				render := func(t *Template) string {
					if t == nil {
						return ""
					}
					s, err := t.String(r.Data)
					if err != nil {
						errs = append(errs, err)
					}
					return strings.TrimSpace(s)
				}
				out := map[string]any{
					"title": render(title), "ci": render(ci), "signal": render(signal), "method": method,
					"severity": render(sev), "status": render(status), "external_id": render(ext), "value": render(value),
				}
				if l, ok := r.Data["labels"].(map[string]any); ok {
					out["labels"] = deepCopy(l)
				}
				if desc != nil {
					out["description"] = render(desc)
				}
				if len(fields) > 0 {
					list := make([]any, 0, len(fields))
					for _, f := range fields {
						if v := render(f.val); v != "" {
							list = append(list, map[string]any{"name": f.name, "value": v})
						}
					}
					out["fields"] = list
				}
				if err := errors.Join(errs...); err != nil {
					return err
				}
				if out["title"] == "" {
					return errors.New("title is empty for this record")
				}
				if out["ci"] == "" {
					return errors.New("configuration item is empty for this record")
				}
				s, ok := NormalizeSeverity(out["severity"].(string))
				if !ok {
					return fmt.Errorf("severity %q is not critical, error, warning, low or info", out["severity"])
				}
				out["severity"] = s
				st, ok := NormalizeStatus(out["status"].(string))
				if !ok {
					return fmt.Errorf("status %q is neither firing nor resolved", out["status"])
				}
				out["status"] = st
				// The key leaves the status out, so resolved updates the firing event of the same alert.
				// Without a signal the title tells alerts apart; sources that reword the title on
				// recovery need a signal or an ID.
				switch k := render(key); {
				case k != "":
					out["key"] = eventKey("k", k)
				case out["external_id"] != "":
					out["key"] = eventKey("x", out["external_id"].(string))
				case out["signal"] != "":
					out["key"] = eventKey("s", out["ci"].(string), out["signal"].(string))
				default:
					out["key"] = eventKey("e", out["ci"].(string), out["title"].(string))
				}
				emit(OutMain, Record{Data: out, Raw: r.Raw, Lineage: r.Lineage})
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "out.event", Version: 1, Category: CategoryOutput, Inputs: 1, CanFail: true,
		Title: Text{"Event", "Событие"},
		Description: Text{
			"Hands the normalized events over to Umbrella. In test runs nothing is written: the events are shown instead.",
			"Передаёт нормализованные события в Umbrella. При тестовом прогоне ничего не записывается: события только показываются.",
		},
		Params: []Param{},
		compile: func(c *compiler, n Node) Step {
			return func(x *Exec, r Record, emit func(string, Record)) error {
				ev, err := EventFromData(r.Data)
				if err != nil {
					return err
				}
				x.res.Events = append(x.res.Events, Emitted{Event: ev, Lineage: r.Lineage, Node: n.ID})
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "ack.response", Version: 1, Category: CategoryConfig, Singleton: true,
		Title: Text{"Response to the source", "Ответ источнику"},
		Description: Text{
			"Only for sources that need a particular answer. Without it the source gets 202 with the request ID.",
			"Только для источников, которым нужен особый ответ. Без него источник получает 202 с ID запроса.",
		},
		Params: []Param{
			{Key: "status", Kind: KindNumber, Default: 202.0, Min: ptr(200), Max: ptr(299), Title: Text{"HTTP status", "HTTP-код"}},
			{Key: "content_type", Kind: KindSelect, Default: "application/json", Title: Text{"Content type", "Тип содержимого"},
				Options: []Option{{"application/json", Text{"JSON", "JSON"}}, {"text/plain", Text{"Text", "Текст"}}}},
			{Key: "body", Kind: KindText, Placeholder: `{"ok": true}`, Title: Text{"Body", "Тело"}},
		},
		compile: func(c *compiler, n Node) Step {
			a := &Ack{Status: int(c.num("status")), ContentType: c.choice("content_type"), Body: c.text("body")}
			if a.ContentType == "application/json" && strings.TrimSpace(a.Body) != "" && !json.Valid([]byte(a.Body)) {
				c.fail("body", "json", "the body is not valid JSON")
			}
			c.ack = a
			return nil
		},
	})
}

func hasIssue(issues []Issue, param string) bool {
	for _, i := range issues {
		if i.Param == param {
			return true
		}
	}
	return false
}

// CompileWebhook compiles only the trigger of a graph: the intake uses it to check requests
// for a connector that is capturing samples but has not been published.
func CompileWebhook(g Graph, opt CompileOptions) (*Webhook, []Issue) {
	for _, n := range g.Nodes {
		if n.Type != "trigger.webhook" {
			continue
		}
		t, ok := TypeOf(n.Type, n.TypeVersion)
		if !ok {
			break
		}
		c := &compiler{node: n, typ: t, credential: opt.Credential}
		t.compile(c, n)
		if HasErrors(c.issues) {
			return nil, c.issues
		}
		return c.webhook, c.issues
	}
	return nil, []Issue{{Level: LevelError, Code: "no_trigger", Message: "the connector has no webhook trigger"}}
}

// Sniff tells whether a body looks like JSON, for the editor's sample view.
func Sniff(body []byte) string {
	b := bytes.TrimSpace(body)
	if len(b) > 0 && (b[0] == '{' || b[0] == '[') && json.Valid(b) {
		return "json"
	}
	return "text"
}
