package flow

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Text processing for sources that send unformatted text: a stack trace of hundreds of lines in
// the description, HTML from a mail gateway, colored console output, "\n" written as two
// characters. transform.text cleans and shortens such text and makes a one-line summary of it;
// the same operations are template filters (${x|firstline}, ${x|truncate:200}).

const (
	// MaxDescription is the longest description an event keeps, in bytes.
	MaxDescription = 64 << 10
	// maxTitle is the longest title an event keeps, in characters; the rest goes to the description.
	maxTitle = 300
	// maxValue is the longest value an event keeps; a longer one goes to the description.
	maxValue      = 300
	maxFields     = 30
	maxFieldName  = 100
	maxFieldValue = 2000
)

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	// htmlBreakRe: tags that end a line in rendered HTML.
	htmlBreakRe = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>|</\s*(?:p|div|li|tr|h[1-6]|pre|table)\s*>`)
	htmlDropRe  = regexp.MustCompile(`(?is)<\s*(script|style)[^>]*>.*?</\s*(?:script|style)\s*>`)
	htmlTagRe   = regexp.MustCompile(`(?s)<[a-zA-Z/!][^>]*>`)
	// errorLineRe: a line that names a failure: the line a summary of a stack trace takes.
	errorLineRe = regexp.MustCompile(`(?i)(exception|error|fatal|panic|traceback|caused by|failed|failure|ошибка|исключение)`)
)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func stripHTML(s string) string {
	s = htmlDropRe.ReplaceAllString(s, "")
	s = htmlBreakRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	return html.UnescapeString(s)
}

// unescapeText turns the escapes a sender wrote as text (\n, \r\n, \t, \") into the characters.
var unescaper = strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\r`, "\n", `\t`, "\t", `\"`, `"`)

func unescapeText(s string) string { return unescaper.Replace(s) }

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n")
}

// oneLine joins the words of the text with single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncateText cuts the text to n characters and marks the cut with an ellipsis.
func truncateText(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:max(n-1, 0)]), " \t\n") + "…"
}

// truncateBytes cuts the text to at most n bytes on a character boundary and says how much was cut.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - 64
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n… (%d more bytes)", len(s)-cut)
}

// firstLine is the first line that is not blank, trimmed.
func firstLine(s string) string {
	for _, l := range splitLines(s) {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := splitLines(s)
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// errorLine is the first line that names a failure (exception, error, panic…), else the first line.
func errorLine(s string) string {
	for _, l := range splitLines(s) {
		if l = strings.TrimSpace(l); l != "" && errorLineRe.MatchString(l) {
			return l
		}
	}
	return firstLine(s)
}

// lastErrorLine is the last line that names a failure: the root cause of a chained stack trace
// ("Caused by: …"), else the first line.
func lastErrorLine(s string) string {
	lines := splitLines(s)
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && errorLineRe.MatchString(l) {
			return l
		}
	}
	return firstLine(s)
}

// headTail keeps the first head and the last tail lines; the lines in between are replaced by
// the marker, where {n} is how many were left out. Zero keeps all lines on that side.
func headTail(lines []string, head, tail int, marker string) []string {
	if head <= 0 && tail <= 0 || len(lines) <= head+tail {
		return lines
	}
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	n := len(lines) - head - tail
	out := append([]string{}, lines[:head]...)
	if marker != "" {
		out = append(out, strings.ReplaceAll(marker, "{n}", strconv.Itoa(n)))
	}
	return append(out, lines[len(lines)-tail:]...)
}

// summaryOf picks the one-line summary of a text by mode.
func summaryOf(s, mode string) string {
	switch mode {
	case "last_line":
		return lastLine(s)
	case "error_line":
		return errorLine(s)
	case "last_error_line":
		return lastErrorLine(s)
	}
	return firstLine(s)
}

// textFilters are the template filters that work on text; numeric ones take a positive count.
var textFilters = map[string]bool{
	"firstline": false, "lastline": false, "errorline": false, "oneline": false, "nohtml": false, "noansi": false, "unescape": false,
	"truncate": true, "head": true, "tail": true,
}

func init() {
	for name := range textFilters {
		filters[name] = true
	}
}

func checkTextFilter(name, arg string) error {
	if !textFilters[name] {
		return nil
	}
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err != nil || n <= 0 {
		return fmt.Errorf("filter %s needs a positive number, as in %s:200", name, name)
	}
	return nil
}

func applyTextFilter(name, arg string, v any) (any, bool) {
	if _, ok := textFilters[name]; !ok {
		return nil, false
	}
	s := Stringify(v)
	n, _ := strconv.Atoi(strings.TrimSpace(arg))
	switch name {
	case "firstline":
		return firstLine(s), true
	case "lastline":
		return lastLine(s), true
	case "errorline":
		return errorLine(s), true
	case "oneline":
		return oneLine(s), true
	case "nohtml":
		return stripHTML(s), true
	case "noansi":
		return stripANSI(s), true
	case "unescape":
		return unescapeText(s), true
	case "truncate":
		return truncateText(s, n), true
	case "head":
		return strings.Join(headTail(splitLines(s), n, 0, "…"), "\n"), true
	case "tail":
		lines := splitLines(s)
		return strings.Join(lines[max(len(lines)-n, 0):], "\n"), true
	}
	return nil, false
}

// textOptions is the compiled configuration of transform.text.
type textOptions struct {
	unescape, ansi, html, single, trim bool
	replace                            []textReplace
	drop, keep                         []*regexp.Regexp
	blank                              string
	extract                            *regexp.Regexp
	head, tail                         int
	marker                             string
	maxChars                           int
}

type textReplace struct {
	re   *regexp.Regexp
	with string
}

func (o *textOptions) apply(s string) string {
	if o.unescape {
		s = unescapeText(s)
	}
	if o.ansi {
		s = stripANSI(s)
	}
	if o.html {
		s = stripHTML(s)
	}
	for _, r := range o.replace {
		s = r.re.ReplaceAllString(s, r.with)
	}
	if o.extract != nil {
		if m := o.extract.FindStringSubmatchIndex(s); m != nil {
			i := 0
			if g := o.extract.SubexpIndex("text"); g > 0 && m[2*g] >= 0 {
				i = g
			} else if o.extract.NumSubexp() > 0 && m[2] >= 0 {
				i = 1
			}
			s = s[m[2*i]:m[2*i+1]]
		}
	}
	lines := splitLines(s)
	out := lines[:0:0]
	for _, l := range lines {
		if o.trim {
			l = strings.TrimRight(l, " \t")
		}
		if matchAny(o.drop, l) || len(o.keep) > 0 && strings.TrimSpace(l) != "" && !matchAny(o.keep, l) {
			continue
		}
		blank := strings.TrimSpace(l) == ""
		switch {
		case blank && o.blank == "drop":
			continue
		case blank && o.blank == "squeeze" && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == ""):
			continue
		}
		out = append(out, l)
	}
	if o.blank == "squeeze" {
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
	}
	out = headTail(out, o.head, o.tail, o.marker)
	if o.single {
		s = oneLine(strings.Join(out, " "))
	} else {
		s = strings.Join(out, "\n")
	}
	if o.trim {
		s = strings.TrimSpace(s)
	}
	return truncateText(s, o.maxChars)
}

func matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// compileRegexps compiles one regular expression per entry of a list parameter.
func (c *compiler) regexps(key string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for i, s := range c.list(key) {
		re, err := regexp.Compile(s)
		if err != nil {
			c.fail(key, "regex", "line %d: %v", i+1, err)
			continue
		}
		out = append(out, re)
	}
	return out
}

func (c *compiler) regex(key string) *regexp.Regexp {
	s := c.text(key)
	if strings.TrimSpace(s) == "" {
		if c.spec(key).Required {
			c.fail(key, "required", "the parameter is required")
		}
		return nil
	}
	re, err := regexp.Compile(s)
	if err != nil {
		c.fail(key, "regex", "%v", err)
		return nil
	}
	return re
}

var summaryOptions = []Option{
	{"first_line", Text{"First line", "Первая строка"}},
	{"error_line", Text{"First line with an error (exception, error, panic…)", "Первая строка с ошибкой (exception, error, panic…)"}},
	{"last_error_line", Text{"Last line with an error (root cause, Caused by)", "Последняя строка с ошибкой (первопричина, Caused by)"}},
	{"last_line", Text{"Last line", "Последняя строка"}},
}

func init() {
	register(&NodeType{
		Type: "transform.text", Version: 1, Category: CategoryTransform, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Text processing", "Обработка текста"},
		Description: Text{
			"Cleans and shortens long or unformatted text (a stack trace, HTML, console output) and makes a one-line summary of it. Steps run in the order of the fields.",
			"Очищает и сокращает длинный или неформатированный текст (стектрейс, HTML, вывод консоли) и делает из него краткую выжимку в одну строку. Шаги выполняются в порядке полей.",
		},
		Params: []Param{
			{Key: "source", Kind: KindTemplate, Required: true, Placeholder: "${annotations.description|$message}",
				Title: Text{"Text", "Текст"}},
			{Key: "target", Kind: KindPath, Default: "text",
				Title: Text{"Write to field", "Записать в поле"},
				Help:  Text{"Later nodes read it as ${text}, for example the description in “Event mapping”.", "Следующие узлы читают его как ${text}, например описание в «Сопоставлении события»."}},
			{Key: "unescape", Kind: KindBool, Default: false,
				Title: Text{`Turn \n and \t written as text into line breaks and tabs`, `Превращать \n и \t, записанные текстом, в переводы строк и табуляции`}},
			{Key: "strip_ansi", Kind: KindBool, Default: true,
				Title: Text{"Remove terminal colors (ANSI)", "Убирать цвета терминала (ANSI)"}},
			{Key: "strip_html", Kind: KindBool, Default: false,
				Title: Text{"Remove HTML tags", "Убирать HTML-теги"},
				Help:  Text{"Line breaks of <br>, <p> and <div> are kept, entities are decoded.", "Переводы строк от <br>, <p> и <div> сохраняются, сущности раскодируются."}},
			{Key: "replace", Kind: KindTable,
				Title: Text{"Replace", "Замены"},
				Help:  Text{"Regular expressions (RE2); $1 or ${name} in the replacement inserts a group.", "Регулярные выражения (RE2); $1 или ${name} в замене подставляют группу."},
				Columns: []Param{
					{Key: "pattern", Kind: KindString, Required: true, Title: Text{"Pattern", "Шаблон"}, Placeholder: `password=\S+`},
					{Key: "with", Kind: KindString, Title: Text{"Replacement", "Замена"}, Placeholder: "password=***"},
				}},
			{Key: "extract", Kind: KindString, Placeholder: `(?s)Exception:(?P<text>.*?)\n\s+at `,
				Title: Text{"Take only the part matching", "Брать только часть, подходящую под"},
				Help: Text{"A regular expression: its group named text, else its first group, else the whole match is kept. No match: the text stays as is.",
					"Регулярное выражение: остаётся его группа text, иначе первая группа, иначе всё совпадение. Нет совпадения: текст не меняется."}},
			{Key: "drop_lines", Kind: KindList, Placeholder: `^\s+at (java|javax|sun|org\.springframework)\.`,
				Title: Text{"Drop lines matching", "Удалять строки, подходящие под"},
				Help:  Text{"One regular expression per line, for example the frames of a framework in a stack trace.", "По регулярному выражению на строку, например кадры фреймворка в стектрейсе."}},
			{Key: "keep_lines", Kind: KindList, Placeholder: `(?i)error|exception|caused by|com\.example`,
				Title: Text{"Keep only lines matching", "Оставлять только строки, подходящие под"},
				Help:  Text{"Empty: all lines are kept.", "Пусто: остаются все строки."}},
			{Key: "blank_lines", Kind: KindSelect, Default: "squeeze",
				Title: Text{"Blank lines", "Пустые строки"},
				Options: []Option{
					{"squeeze", Text{"Keep one in a row", "Оставлять одну подряд"}},
					{"drop", Text{"Remove", "Удалять"}},
					{"keep", Text{"Keep as is", "Оставлять как есть"}},
				}},
			{Key: "trim", Kind: KindBool, Default: true,
				Title: Text{"Trim spaces at the ends of lines and of the text", "Обрезать пробелы в концах строк и текста"}},
			{Key: "head_lines", Kind: KindNumber, Default: 0.0, Min: ptr(0), Max: ptr(10000),
				Title: Text{"Keep the first lines", "Оставлять первые строки"},
				Help:  Text{"0: no limit. With the last lines, the middle is replaced by the marker.", "0: без ограничения. Вместе с последними строками середина заменяется пометкой."}},
			{Key: "tail_lines", Kind: KindNumber, Default: 0.0, Min: ptr(0), Max: ptr(10000),
				Title: Text{"Keep the last lines", "Оставлять последние строки"}},
			{Key: "skip_marker", Kind: KindString, Default: "… {n} lines skipped …",
				Title: Text{"Marker of skipped lines", "Пометка пропущенных строк"},
				Help:  Text{"{n} is the number of lines left out. Empty: no marker.", "{n} — число пропущенных строк. Пусто: без пометки."}},
			{Key: "single_line", Kind: KindBool, Default: false,
				Title: Text{"Join into one line", "Склеивать в одну строку"}},
			{Key: "max_chars", Kind: KindNumber, Default: 0.0, Min: ptr(0), Max: ptr(MaxDescription),
				Title: Text{"Characters at most", "Не длиннее, символов"},
				Help:  Text{"0: no limit. A longer text is cut and ends with an ellipsis.", "0: без ограничения. Более длинный текст обрезается и заканчивается многоточием."}},
			{Key: "summary_target", Kind: KindPath, Placeholder: "summary",
				Title: Text{"Write a summary to field", "Записать краткую выжимку в поле"},
				Help:  Text{"One line taken from the processed text, for the incident title. Empty: no summary.", "Одна строка из обработанного текста, для заголовка инцидента. Пусто: без выжимки."}},
			{Key: "summary", Kind: KindSelect, Default: "error_line", Options: summaryOptions,
				Title: Text{"Summary line", "Строка выжимки"}},
			{Key: "summary_max", Kind: KindNumber, Default: 200.0, Min: ptr(20), Max: ptr(maxTitle),
				Title: Text{"Summary length, characters", "Длина выжимки, символов"}},
		},
		compile: func(c *compiler, n Node) Step {
			src := c.template("source")
			target := c.pathOr("target", "text")
			o := &textOptions{
				unescape: c.boolean("unescape"), ansi: c.boolean("strip_ansi"), html: c.boolean("strip_html"),
				single: c.boolean("single_line"), trim: c.boolean("trim"),
				drop: c.regexps("drop_lines"), keep: c.regexps("keep_lines"), blank: c.choice("blank_lines"),
				extract: c.regex("extract"), head: int(c.num("head_lines")), tail: int(c.num("tail_lines")),
				marker: c.text("skip_marker"), maxChars: int(c.num("max_chars")),
			}
			for i, row := range c.table("replace") {
				re, err := regexp.Compile(row["pattern"])
				if err != nil {
					c.fail("replace", "regex", "row %d: %v", i+1, err)
					continue
				}
				o.replace = append(o.replace, textReplace{re, row["with"]})
			}
			summaryTarget := c.path("summary_target")
			mode := c.choice("summary")
			summaryMax := int(c.num("summary_max"))
			return func(x *Exec, r Record, emit func(string, Record)) error {
				if src == nil {
					return errors.New("the text is not set")
				}
				v, err := src.String(r.Data)
				if err != nil {
					return err
				}
				out := o.apply(v)
				r = r.clone()
				if err := target.Set(r.Data, out); err != nil {
					return err
				}
				if summaryTarget != nil {
					if err := summaryTarget.Set(r.Data, truncateText(oneLine(summaryOf(out, mode)), summaryMax)); err != nil {
						return err
					}
				}
				emit(OutMain, r)
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "parse.regex", Version: 1, Category: CategoryParse, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Parse by regular expression", "Разбор регулярным выражением"},
		Description: Text{
			"Takes fields out of unstructured text: every named group (?P<name>…) of the expression becomes a field.",
			"Достаёт поля из неструктурированного текста: каждая именованная группа (?P<name>…) выражения становится полем.",
		},
		Params: []Param{
			{Key: "source", Kind: KindTemplate, Required: true, Placeholder: "${message}",
				Title: Text{"Text", "Текст"}},
			{Key: "pattern", Kind: KindString, Required: true, Placeholder: `(?P<host>[\w.-]+): (?P<error>.+)`,
				Title: Text{"Regular expression (RE2)", "Регулярное выражение (RE2)"}},
			{Key: "target", Kind: KindPath, Placeholder: "parsed",
				Title: Text{"Write groups into field", "Записать группы в поле"},
				Help:  Text{"Empty: the groups become fields of the record itself.", "Пусто: группы становятся полями самой записи."}},
			{Key: "all", Kind: KindBool, Default: false,
				Title: Text{"All matches as a list", "Все совпадения списком"},
				Help:  Text{"The field then holds a list with an object per match.", "Тогда поле содержит список с объектом на каждое совпадение."}},
			{Key: "no_match", Kind: KindSelect, Default: "pass",
				Title: Text{"When nothing matches", "Если совпадений нет"},
				Options: []Option{
					{"pass", Text{"Pass the record on unchanged", "Передать запись дальше без изменений"}},
					{"drop", Text{"Drop the record", "Отбросить запись"}},
					{"fail", Text{"Record an error", "Записать ошибку"}},
				}},
		},
		compile: func(c *compiler, n Node) Step {
			src := c.template("source")
			re := c.regex("pattern")
			if re != nil && !hasNamedGroup(re) {
				c.fail("pattern", "regex", "the expression has no named group (?P<name>…)")
			}
			target := c.path("target")
			all := c.boolean("all")
			noMatch := c.choice("no_match")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				if src == nil || re == nil {
					return errors.New("the node is not configured")
				}
				s, err := src.String(r.Data)
				if err != nil {
					return err
				}
				groups := func(m []string) map[string]any {
					out := map[string]any{}
					for i, name := range re.SubexpNames() {
						if name != "" && i < len(m) {
							out[name] = m[i]
						}
					}
					return out
				}
				var matches [][]string
				if all {
					matches = re.FindAllStringSubmatch(s, 1000)
				} else if m := re.FindStringSubmatch(s); m != nil {
					matches = [][]string{m}
				}
				if len(matches) == 0 {
					switch noMatch {
					case "drop":
						x.res.Filtered++
						return nil
					case "fail":
						return fmt.Errorf("the text does not match %s", re)
					}
					emit(OutMain, r)
					return nil
				}
				r = r.clone()
				if all {
					list := make([]any, 0, len(matches))
					for _, m := range matches {
						list = append(list, groups(m))
					}
					if target == nil {
						return errors.New("all matches need a field to write the list into")
					}
					if err := target.Set(r.Data, list); err != nil {
						return err
					}
				} else if target != nil {
					if err := target.Set(r.Data, groups(matches[0])); err != nil {
						return err
					}
				} else {
					for k, v := range groups(matches[0]) {
						r.Data[k] = v
					}
				}
				emit(OutMain, r)
				return nil
			}
		},
	})

	register(&NodeType{
		Type: "parse.kv", Version: 1, Category: CategoryParse, Inputs: 1, Outputs: []string{OutMain}, CanFail: true,
		Title: Text{"Parse key=value", "Разбор key=value"},
		Description: Text{
			"Splits text like host=db-01 level=error msg=\"disk full\" into fields. Quoted values may hold spaces.",
			"Разбивает текст вида host=db-01 level=error msg=\"disk full\" на поля. Значения в кавычках могут содержать пробелы.",
		},
		Params: []Param{
			{Key: "source", Kind: KindTemplate, Required: true, Placeholder: "${valueString}",
				Title: Text{"Text", "Текст"}},
			{Key: "separator", Kind: KindString, Default: "=",
				Title: Text{"Between key and value", "Между ключом и значением"}},
			{Key: "target", Kind: KindPath, Default: "kv",
				Title: Text{"Write into field", "Записать в поле"}},
		},
		compile: func(c *compiler, n Node) Step {
			src := c.template("source")
			sep := c.text("separator")
			if sep == "" {
				c.fail("separator", "required", "the parameter is required")
			}
			target := c.pathOr("target", "kv")
			return func(x *Exec, r Record, emit func(string, Record)) error {
				if src == nil || sep == "" {
					return errors.New("the node is not configured")
				}
				s, err := src.String(r.Data)
				if err != nil {
					return err
				}
				r = r.clone()
				if err := target.Set(r.Data, parseKV(s, sep)); err != nil {
					return err
				}
				emit(OutMain, r)
				return nil
			}
		},
	})
}

func hasNamedGroup(re *regexp.Regexp) bool {
	for _, n := range re.SubexpNames() {
		if n != "" {
			return true
		}
	}
	return false
}

// parseKV reads key<sep>value pairs separated by spaces, commas or semicolons; a value may be
// quoted with " or ' and then holds anything but its quote. Words without the separator are skipped.
func parseKV(s, sep string) map[string]any {
	out := map[string]any{}
	i := 0
	isGap := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == ';' }
	for i < len(s) {
		for i < len(s) && isGap(s[i]) {
			i++
		}
		start := i
		for i < len(s) && !isGap(s[i]) && !strings.HasPrefix(s[i:], sep) {
			i++
		}
		key := strings.Trim(s[start:i], "[]{}()")
		if !strings.HasPrefix(s[i:], sep) {
			continue
		}
		i += len(sep)
		var val string
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			end := strings.IndexByte(s[i+1:], q)
			if end < 0 {
				val, i = s[i+1:], len(s)
			} else {
				val, i = s[i+1:i+1+end], i+end+2
			}
		} else {
			vs := i
			for i < len(s) && !isGap(s[i]) {
				i++
			}
			val = strings.TrimRight(s[vs:i], "]})")
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}
