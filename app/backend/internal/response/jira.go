package response

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Jira is a Jira Cloud REST API v3 client: basic authentication with the e-mail of an account
// and its API token.
type Jira struct {
	set    model.JiraSettings
	token  string
	client *http.Client
}

func NewJira(set model.JiraSettings, token string, client *http.Client) *Jira {
	return &Jira{set: set, token: token, client: client}
}

// Issue is a created issue.
type Issue struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// APIError is an answer of an external API that is not a success.
type APIError struct {
	Service string
	Status  int
	Msg     string
}

func (e *APIError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("%s: %d %s", e.Service, e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("%s: %d %s", e.Service, e.Status, e.Msg)
}

// call sends a JSON request and decodes a JSON answer into out (when not nil).
func call(ctx context.Context, client *http.Client, service, method, endpoint string, header http.Header, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s: %v", service, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return &APIError{Service: service, Status: resp.StatusCode, Msg: errorMessage(data)}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s: unexpected answer: %v", service, err)
		}
	}
	return nil
}

// errorMessage takes the message out of the error answers of Jira, Graph and Zoom.
func errorMessage(data []byte) string {
	var v struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
		Error         json.RawMessage   `json:"error"`
		Description   string            `json:"error_description"`
		Message       string            `json:"message"`
	}
	if json.Unmarshal(data, &v) != nil {
		t := strings.TrimSpace(string(data))
		if len(t) <= 200 && !strings.ContainsAny(t, "<{") {
			return t
		}
		return ""
	}
	var parts []string
	parts = append(parts, v.ErrorMessages...)
	for k, m := range v.Errors {
		parts = append(parts, k+": "+m)
	}
	if v.Description != "" {
		parts = append(parts, v.Description)
	}
	if len(v.Error) > 0 {
		var g struct {
			Message string `json:"message"`
		}
		var s string
		if json.Unmarshal(v.Error, &g) == nil && g.Message != "" {
			parts = append(parts, g.Message)
		} else if json.Unmarshal(v.Error, &s) == nil && s != "" && v.Description == "" {
			parts = append(parts, s)
		}
	}
	if v.Message != "" {
		parts = append(parts, v.Message)
	}
	return strings.Join(parts, "; ")
}

func (j *Jira) header() http.Header {
	h := http.Header{}
	req := &http.Request{Header: h}
	req.SetBasicAuth(j.set.Email, j.token)
	return h
}

func (j *Jira) do(ctx context.Context, method, path string, body, out any) error {
	return call(ctx, j.client, "Jira", method, j.set.API()+path, j.header(), body, out)
}

// Browse is the address of an issue in the browser.
func (j *Jira) Browse(key string) string { return j.set.API() + "/browse/" + url.PathEscape(key) }

// Check checks the account and the project and returns the name of the account.
func (j *Jira) Check(ctx context.Context) (string, error) {
	var me struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := j.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &me); err != nil {
		return "", err
	}
	if j.set.Project != "" {
		var p struct {
			Name string `json:"name"`
		}
		if err := j.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(j.set.Project), nil, &p); err != nil {
			return "", err
		}
		return me.DisplayName + " · " + p.Name, nil
	}
	return me.DisplayName, nil
}

// NewIssue is what an issue is created with.
type NewIssue struct {
	Type     string
	Summary  string
	Priority string
	Labels   []string
	// Due is YYYY-MM-DD; empty sets none.
	Due  string
	Body Doc
}

// Create creates an issue in the project. A field the create screen of the project does not
// have (priority, due date) is dropped and the issue created without it.
func (j *Jira) Create(ctx context.Context, in NewIssue) (Issue, error) {
	fields := map[string]any{
		"project":     map[string]string{"key": j.set.Project},
		"issuetype":   map[string]string{"name": in.Type},
		"summary":     truncate(in.Summary, 250),
		"description": in.Body.ADF(),
	}
	if len(in.Labels) > 0 {
		fields["labels"] = in.Labels
	}
	if in.Priority != "" {
		fields["priority"] = map[string]string{"name": in.Priority}
	}
	if in.Due != "" {
		fields["duedate"] = in.Due
	}
	var out struct {
		Key string `json:"key"`
	}
	for range 3 {
		err := j.do(ctx, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": fields}, &out)
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusBadRequest {
			dropped := false
			for _, f := range []string{"priority", "duedate", "labels"} {
				if _, ok := fields[f]; ok && strings.Contains(api.Msg, f) {
					delete(fields, f)
					dropped = true
				}
			}
			if dropped {
				continue
			}
		}
		if err != nil {
			return Issue{}, err
		}
		return Issue{Key: out.Key, URL: j.Browse(out.Key)}, nil
	}
	return Issue{}, errors.New("Jira: the issue was not created")
}

// Comment adds a comment to an issue.
func (j *Jira) Comment(ctx context.Context, key string, body Doc) error {
	return j.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", map[string]any{"body": body.ADF()}, nil)
}

// Link links two issues: inward is the postmortem, outward the task.
func (j *Jira) Link(ctx context.Context, linkType, inward, outward string) error {
	return j.do(ctx, http.MethodPost, "/rest/api/3/issueLink", map[string]any{
		"type": map[string]string{"name": linkType}, "inwardIssue": map[string]string{"key": inward}, "outwardIssue": map[string]string{"key": outward}}, nil)
}

// Transition moves an issue by the transition of that name (case does not matter).
func (j *Jira) Transition(ctx context.Context, key, name string) error {
	var list struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := j.do(ctx, http.MethodGet, path, nil, &list); err != nil {
		return err
	}
	for _, t := range list.Transitions {
		if strings.EqualFold(t.Name, name) || strings.EqualFold(t.To.Name, name) {
			return j.do(ctx, http.MethodPost, path, map[string]any{"transition": map[string]string{"id": t.ID}}, nil)
		}
	}
	return fmt.Errorf("Jira: issue %s has no transition %q", key, name)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Doc is a rich text document: headings, paragraphs, bullet lists and links. It is written as
// Atlassian Document Format for Jira and as HTML for Teams chats.
type Doc []Block

// Block is one block of a document.
type Block struct {
	// Kind: h (heading), p (paragraph), ul (bullet list).
	Kind  string
	Text  string
	Items []string
	// Link makes the paragraph a link with Text as its title.
	Link string
}

func H(text string) Block             { return Block{Kind: "h", Text: text} }
func P(text string) Block             { return Block{Kind: "p", Text: text} }
func UL(items ...string) Block        { return Block{Kind: "ul", Items: items} }
func Link(title, href string) Block   { return Block{Kind: "p", Text: title, Link: href} }
func adfText(t string) map[string]any { return map[string]any{"type": "text", "text": t} }
func adfPara(c ...any) map[string]any { return map[string]any{"type": "paragraph", "content": c} }

// ADF is the document in Atlassian Document Format.
func (d Doc) ADF() map[string]any {
	content := []any{}
	for _, b := range d {
		switch b.Kind {
		case "h":
			content = append(content, map[string]any{"type": "heading", "attrs": map[string]int{"level": 3}, "content": []any{adfText(cmp.Or(b.Text, " "))}})
		case "ul":
			if len(b.Items) == 0 {
				continue
			}
			items := []any{}
			for _, it := range b.Items {
				items = append(items, map[string]any{"type": "listItem", "content": []any{adfPara(adfText(cmp.Or(it, " ")))}})
			}
			content = append(content, map[string]any{"type": "bulletList", "content": items})
		default:
			if b.Text == "" {
				continue
			}
			t := adfText(b.Text)
			if b.Link != "" {
				t["marks"] = []any{map[string]any{"type": "link", "attrs": map[string]string{"href": b.Link}}}
			}
			content = append(content, adfPara(t))
		}
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// HTML is the document as HTML (Teams chat messages).
func (d Doc) HTML() string {
	var b strings.Builder
	for _, bl := range d {
		switch bl.Kind {
		case "h":
			b.WriteString("<p><b>" + escape(bl.Text) + "</b></p>")
		case "ul":
			if len(bl.Items) == 0 {
				continue
			}
			b.WriteString("<ul>")
			for _, it := range bl.Items {
				b.WriteString("<li>" + escape(it) + "</li>")
			}
			b.WriteString("</ul>")
		default:
			if bl.Text == "" {
				continue
			}
			if bl.Link != "" {
				b.WriteString(`<p><a href="` + escape(bl.Link) + `">` + escape(bl.Text) + "</a></p>")
			} else {
				b.WriteString("<p>" + escape(bl.Text) + "</p>")
			}
		}
	}
	return b.String()
}

// Plain is the document as plain text.
func (d Doc) Plain() string {
	var b strings.Builder
	for _, bl := range d {
		switch bl.Kind {
		case "h":
			b.WriteString("\n" + bl.Text + "\n")
		case "ul":
			for _, it := range bl.Items {
				b.WriteString("• " + it + "\n")
			}
		default:
			if bl.Text == "" {
				continue
			}
			if bl.Link != "" {
				b.WriteString(bl.Text + ": " + bl.Link + "\n")
			} else {
				b.WriteString(bl.Text + "\n")
			}
		}
	}
	return strings.TrimLeft(b.String(), "\n")
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
