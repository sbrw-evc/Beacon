package response

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Graph is a Microsoft Graph client acting as the service account of the settings: the
// refresh token of the account is exchanged for access tokens; Microsoft rotates it, and the new
// one is handed to Rotated to be stored.
type Graph struct {
	set     model.GraphSettings
	secret  string
	client  *http.Client
	Rotated func(refresh string)

	mu      sync.Mutex
	refresh string
	access  string
	expires time.Time
	// appAccess is the token of the app itself (calls).
	appAccess  string
	appExpires time.Time
}

func NewGraph(set model.GraphSettings, clientSecret, refreshToken string, client *http.Client) *Graph {
	return &Graph{set: set, secret: clientSecret, refresh: refreshToken, client: client}
}

func (g *Graph) token(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.access != "" && time.Now().Before(g.expires) {
		return g.access, nil
	}
	form := url.Values{
		"client_id":     {g.set.ClientID},
		"client_secret": {g.secret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {g.refresh},
		"scope":         {"https://graph.microsoft.com/.default offline_access"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.set.Login()+"/"+url.PathEscape(g.set.TenantID)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Microsoft sign-in: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return "", &APIError{Service: "Microsoft sign-in", Status: resp.StatusCode, Msg: errorMessage(raw)}
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Access == "" {
		return "", fmt.Errorf("Microsoft sign-in: no access token in the answer")
	}
	g.access, g.expires = out.Access, time.Now().Add(time.Duration(max(out.ExpiresIn-120, 60))*time.Second)
	if out.Refresh != "" && out.Refresh != g.refresh {
		g.refresh = out.Refresh
		if g.Rotated != nil {
			g.Rotated(out.Refresh)
		}
	}
	return g.access, nil
}

func (g *Graph) do(ctx context.Context, method, path string, body, out any) error {
	tok, err := g.token(ctx)
	if err != nil {
		return err
	}
	return call(ctx, g.client, "Microsoft Graph", method, g.set.API()+path, http.Header{"Authorization": {"Bearer " + tok}}, body, out)
}

// GraphUser is an account of the tenant.
type GraphUser struct {
	ID   string `json:"id"`
	UPN  string `json:"userPrincipalName"`
	Name string `json:"displayName"`
}

// Me is the service account.
func (g *Graph) Me(ctx context.Context) (GraphUser, error) {
	var u GraphUser
	err := g.do(ctx, http.MethodGet, "/me?$select=id,userPrincipalName,displayName", nil, &u)
	return u, err
}

// User finds an account by its user principal name or e-mail.
func (g *Graph) User(ctx context.Context, upn string) (GraphUser, error) {
	var u GraphUser
	err := g.do(ctx, http.MethodGet, "/users/"+url.PathEscape(upn)+"?$select=id,userPrincipalName,displayName", nil, &u)
	return u, err
}

func (g *Graph) member(id string) map[string]any {
	return map[string]any{
		"@odata.type":     "#microsoft.graph.aadUserConversationMember",
		"roles":           []string{"owner"},
		"user@odata.bind": g.set.API() + "/users('" + id + "')",
	}
}

// Chat is a Teams chat.
type Chat struct {
	ID  string `json:"id"`
	URL string `json:"webUrl"`
}

// CreateChat opens a group chat with a topic; the service account is a member, memberIDs are
// the Graph IDs of the others.
func (g *Graph) CreateChat(ctx context.Context, topic string, memberIDs []string) (Chat, error) {
	me, err := g.Me(ctx)
	if err != nil {
		return Chat{}, err
	}
	members := []any{g.member(me.ID)}
	for _, id := range memberIDs {
		if id != me.ID {
			members = append(members, g.member(id))
		}
	}
	var c Chat
	err = g.do(ctx, http.MethodPost, "/chats", map[string]any{"chatType": "group", "topic": truncate(topic, 250), "members": members}, &c)
	return c, err
}

// AddMember adds an account to a chat with the whole history visible.
func (g *Graph) AddMember(ctx context.Context, chatID, userID string) error {
	m := g.member(userID)
	m["visibleHistoryStartDateTime"] = "0001-01-01T00:00:00Z"
	return g.do(ctx, http.MethodPost, "/chats/"+url.PathEscape(chatID)+"/members", m, nil)
}

// Post sends an HTML message to a chat.
func (g *Graph) Post(ctx context.Context, chatID, html string) error {
	return g.do(ctx, http.MethodPost, "/chats/"+url.PathEscape(chatID)+"/messages", map[string]any{"body": map[string]string{"contentType": "html", "content": html}}, nil)
}

// Meeting is a conference call.
type Meeting struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreateMeeting opens a Teams online meeting for the next hours.
func (g *Graph) CreateMeeting(ctx context.Context, subject string, now time.Time) (Meeting, error) {
	var out struct {
		ID   string `json:"id"`
		Join string `json:"joinWebUrl"`
	}
	err := g.do(ctx, http.MethodPost, "/me/onlineMeetings", map[string]any{
		"subject": truncate(subject, 250), "startDateTime": now.UTC().Format(time.RFC3339), "endDateTime": now.Add(4 * time.Hour).UTC().Format(time.RFC3339),
	}, &out)
	return Meeting{ID: out.ID, URL: out.Join}, err
}

// Zoom is a Zoom API client of a Server-to-Server OAuth app.
type Zoom struct {
	set    model.ZoomAPISettings
	secret string
	client *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
}

func NewZoom(set model.ZoomAPISettings, clientSecret string, client *http.Client) *Zoom {
	return &Zoom{set: set, secret: clientSecret, client: client}
}

func (z *Zoom) token(ctx context.Context) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.access != "" && time.Now().Before(z.expires) {
		return z.access, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.set.OAuth()+"/oauth/token?grant_type=account_credentials&account_id="+url.QueryEscape(z.set.AccountID), nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(z.set.ClientID, z.secret)
	var out struct {
		Access    string `json:"access_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := call(ctx, z.client, "Zoom sign-in", http.MethodPost, req.URL.String(), req.Header, nil, &out); err != nil {
		return "", err
	}
	if out.Access == "" {
		return "", fmt.Errorf("Zoom sign-in: no access token in the answer")
	}
	z.access, z.expires = out.Access, time.Now().Add(time.Duration(max(out.ExpiresIn-120, 60))*time.Second)
	return z.access, nil
}

func (z *Zoom) do(ctx context.Context, method, path string, body, out any) error {
	tok, err := z.token(ctx)
	if err != nil {
		return err
	}
	return call(ctx, z.client, "Zoom", method, z.set.API()+path, http.Header{"Authorization": {"Bearer " + tok}}, body, out)
}

// Check reads the meeting host and returns its e-mail.
func (z *Zoom) Check(ctx context.Context) (string, error) {
	var u struct {
		Email string `json:"email"`
	}
	err := z.do(ctx, http.MethodGet, "/users/"+url.PathEscape(z.set.User), nil, &u)
	return u.Email, err
}

// CreateMeeting opens an instant meeting anyone with the link joins without waiting for the host.
func (z *Zoom) CreateMeeting(ctx context.Context, topic string) (Meeting, error) {
	var out struct {
		ID   json.Number `json:"id"`
		Join string      `json:"join_url"`
	}
	err := z.do(ctx, http.MethodPost, "/users/"+url.PathEscape(z.set.User)+"/meetings", map[string]any{
		"topic": truncate(topic, 200), "type": 1, "settings": map[string]any{"join_before_host": true, "waiting_room": false},
	}, &out)
	return Meeting{ID: out.ID.String(), URL: out.Join}, err
}

// appToken is an access token of the app itself (client credentials): Teams calls are placed by
// the app (application permission Calls.Initiate.All), not by the service account.
func (g *Graph) appToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.appAccess != "" && time.Now().Before(g.appExpires) {
		return g.appAccess, nil
	}
	form := url.Values{
		"client_id":     {g.set.ClientID},
		"client_secret": {g.secret},
		"grant_type":    {"client_credentials"},
		"scope":         {"https://graph.microsoft.com/.default"},
	}
	var out struct {
		Access    string `json:"access_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.set.Login()+"/"+url.PathEscape(g.set.TenantID)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Microsoft sign-in: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return "", &APIError{Service: "Microsoft sign-in", Status: resp.StatusCode, Msg: errorMessage(raw)}
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Access == "" {
		return "", fmt.Errorf("Microsoft sign-in: no access token in the answer")
	}
	g.appAccess, g.appExpires = out.Access, time.Now().Add(time.Duration(max(out.ExpiresIn-120, 60))*time.Second)
	return g.appAccess, nil
}

func (g *Graph) doApp(ctx context.Context, method, path string, body, out any) error {
	tok, err := g.appToken(ctx)
	if err != nil {
		return err
	}
	return call(ctx, g.client, "Microsoft Graph", method, g.set.API()+path, http.Header{"Authorization": {"Bearer " + tok}}, body, out)
}

// CallUser rings a Teams user from the app with media hosted by Microsoft (prompts are played
// from audio files); Microsoft posts the progress of the call to callback.
func (g *Graph) CallUser(ctx context.Context, callback, userID, name string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := g.doApp(ctx, http.MethodPost, "/communications/calls", map[string]any{
		"@odata.type": "#microsoft.graph.call",
		"callbackUri": callback,
		"targets": []any{map[string]any{
			"@odata.type": "#microsoft.graph.invitationParticipantInfo",
			"identity": map[string]any{
				"@odata.type": "#microsoft.graph.identitySet",
				"user":        map[string]any{"@odata.type": "#microsoft.graph.identity", "id": userID, "displayName": name},
			},
		}},
		"requestedModalities": []string{"audio"},
		"mediaConfig":         map[string]any{"@odata.type": "#microsoft.graph.serviceHostedMediaConfig"},
		"tenantId":            g.set.TenantID,
	}, &out)
	if err == nil && out.ID == "" {
		err = errors.New("Microsoft Graph: the call has no ID")
	}
	return out.ID, err
}

// PlayPrompt plays audio files one after another in a call; Microsoft fetches them by URL.
func (g *Graph) PlayPrompt(ctx context.Context, callID, clientContext string, uris []string) error {
	prompts := make([]any, 0, len(uris))
	for i, u := range uris {
		prompts = append(prompts, map[string]any{
			"@odata.type": "#microsoft.graph.mediaPrompt",
			"mediaInfo":   map[string]any{"@odata.type": "#microsoft.graph.mediaInfo", "uri": u, "resourceId": fmt.Sprintf("%s-%d", clientContext, i)},
		})
	}
	return g.doApp(ctx, http.MethodPost, "/communications/calls/"+url.PathEscape(callID)+"/playPrompt", map[string]any{"clientContext": clientContext, "prompts": prompts}, nil)
}

// SubscribeToTone asks Microsoft to report the keys pressed in a call.
func (g *Graph) SubscribeToTone(ctx context.Context, callID string) error {
	return g.doApp(ctx, http.MethodPost, "/communications/calls/"+url.PathEscape(callID)+"/subscribeToTone", map[string]any{"clientContext": "tone"}, nil)
}

// Hangup ends a call.
func (g *Graph) Hangup(ctx context.Context, callID string) error {
	return g.doApp(ctx, http.MethodDelete, "/communications/calls/"+url.PathEscape(callID), nil, nil)
}

// SendChat sends a Team Chat message from the host user to a contact (an e-mail).
func (z *Zoom) SendChat(ctx context.Context, to, text string) error {
	return z.do(ctx, http.MethodPost, "/chat/users/"+url.PathEscape(z.set.User)+"/messages", map[string]any{"message": truncate(text, 4000), "to_contact": to}, nil)
}

// SendFile sends a file in Team Chat from the host user to a contact. Files go to a separate
// host of the API (fileapi.zoom.us) unless the API address is replaced.
func (z *Zoom) SendFile(ctx context.Context, to, name, contentType string, data []byte) error {
	tok, err := z.token(ctx)
	if err != nil {
		return err
	}
	base := "https://fileapi.zoom.us/v2"
	if z.set.APIURL != "" {
		base = z.set.API()
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("to_contact", to)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="files"; filename="`+name+`"`)
	h.Set("Content-Type", contentType)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/users/"+url.PathEscape(z.set.User)+"/messages/files", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := z.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("Zoom: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return &APIError{Service: "Zoom", Status: resp.StatusCode, Msg: errorMessage(raw)}
	}
	return nil
}
