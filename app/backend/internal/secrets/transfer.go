package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Check struct {
	OK         bool   `json:"ok"`
	Status     Status `json:"status"`
	WriteOK    bool   `json:"write_ok"`
	WriteError string `json:"write_error,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (c *Client) Verify(ctx context.Context) Check {
	rep := Check{Status: c.Status(ctx)}
	if !rep.Status.TokenOK || !rep.Status.MountOK {
		rep.Error = rep.Status.Error
		return rep
	}
	probe := "umbrella-probe-" + strings.ToLower(rand.Text()[:8])
	if err := c.Put(ctx, probe, map[string]string{"probe": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		rep.WriteError = err.Error()
		rep.Error = "cannot write secrets: " + err.Error()
		if errors.Is(err, ErrNotFound) {
			rep.Error = "the mount " + c.cfg.Mount + " does not exist or is not a KV version 2 secrets engine"
		}
		return rep
	}
	if err := c.Delete(ctx, probe); err != nil {
		rep.WriteError = err.Error()
		rep.Error = "cannot delete secrets: " + err.Error()
		return rep
	}
	rep.WriteOK, rep.OK = true, true
	return rep
}

func (c *Client) CheckKV2(ctx context.Context) error {
	if !c.Enabled() {
		return ErrNotConfigured
	}
	var out struct {
		Data struct {
			Type    string            `json:"type"`
			Options map[string]string `json:"options"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/sys/internal/ui/mounts/"+escapePath(c.cfg.Mount), nil, &out); err != nil {
		return fmt.Errorf("the mount %s is missing or not accessible: %w", c.cfg.Mount, err)
	}
	if out.Data.Type != "kv" || out.Data.Options["version"] != "2" {
		kind := out.Data.Type
		if v := out.Data.Options["version"]; v != "" {
			kind += " v" + v
		}
		return fmt.Errorf("the mount %s is %s, not KV version 2", c.cfg.Mount, kind)
	}
	return nil
}

func (c *Client) List(ctx context.Context) ([]string, error) {
	if !c.Enabled() {
		return nil, ErrNotConfigured
	}
	return c.listUnder(ctx, "")
}

func (c *Client) listUnder(ctx context.Context, prefix string) ([]string, error) {
	var out struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/"+c.cfg.Mount+"/metadata/"+escapePath(prefix)+"?list=true", nil, &out)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s/%s: %w", c.cfg.Mount, prefix, err)
	}
	var paths []string
	for _, k := range out.Data.Keys {
		if !strings.HasSuffix(k, "/") {
			paths = append(paths, prefix+k)
			continue
		}
		nested, err := c.listUnder(ctx, prefix+k)
		if err != nil {
			return nil, err
		}
		paths = append(paths, nested...)
	}
	return paths, nil
}

func (c *Client) CopyTo(ctx context.Context, dst *Client) ([]string, error) {
	paths, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	var copied []string
	for _, p := range paths {
		data, err := c.readRaw(ctx, p)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return copied, fmt.Errorf("read %s/%s: %w", c.cfg.Mount, p, err)
		}
		if err := dst.writeRaw(ctx, p, data); err != nil {
			return copied, fmt.Errorf("write %s/%s: %w", dst.cfg.Mount, p, err)
		}
		copied = append(copied, p)
	}
	return copied, nil
}

func (c *Client) readRaw(ctx context.Context, path string) (map[string]any, error) {
	var out struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/"+c.cfg.Mount+"/data/"+escapePath(path), nil, &out); err != nil {
		return nil, err
	}
	if out.Data.Data == nil {
		return nil, ErrNotFound
	}
	return out.Data.Data, nil
}

func (c *Client) writeRaw(ctx context.Context, path string, data map[string]any) error {
	err := c.do(ctx, http.MethodPost, "/v1/"+c.cfg.Mount+"/data/"+escapePath(path), map[string]any{"data": data}, nil)
	c.mu.Lock()
	delete(c.cache, c.cfg.Mount+"/"+path)
	c.mu.Unlock()
	return err
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func MoveRef(ref, from, to string) (string, bool) {
	mount, path, key, err := ParseRef(ref)
	if err != nil || mount != from || from == to {
		return ref, false
	}
	return Scheme + to + "/" + path + "#" + key, true
}
