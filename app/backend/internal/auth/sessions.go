package auth

import (
	"bytes"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var (
	IdleTimeout = 12 * time.Hour
	MaxLifetime = 7 * 24 * time.Hour
)

const sessionsFile = "sessions.gob"

type Session struct {
	ID       string
	UserID   string
	CSRF     string
	Created  time.Time
	LastSeen time.Time
}

type Sessions struct {
	mu    sync.Mutex
	m     map[string]*Session
	dirty bool
}

func NewSessions() *Sessions { return &Sessions{m: map[string]*Session{}} }

func (s *Sessions) Create(userID string) *Session {
	now := time.Now()
	ss := &Session{ID: RandomToken("", 32), UserID: userID, CSRF: RandomToken("", 24), Created: now, LastSeen: now}
	s.mu.Lock()
	s.m[TokenHash(ss.ID)] = ss
	s.dirty = true
	s.mu.Unlock()
	return ss
}

func (s *Sessions) Get(id string) *Session {
	if id == "" {
		return nil
	}
	key := TokenHash(id)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.m[key]
	if ss == nil {
		return nil
	}
	if now.Sub(ss.LastSeen) > IdleTimeout || now.Sub(ss.Created) > MaxLifetime {
		delete(s.m, key)
		s.dirty = true
		return nil
	}
	if now.Sub(ss.LastSeen) > time.Minute {
		s.dirty = true
	}
	ss.LastSeen = now
	c := *ss
	c.ID = id
	return &c
}

func (s *Sessions) Delete(id string) {
	s.mu.Lock()
	delete(s.m, TokenHash(id))
	s.dirty = true
	s.mu.Unlock()
}

func (s *Sessions) DeleteUser(userID, keep string) {
	k := ""
	if keep != "" {
		k = TokenHash(keep)
	}
	s.mu.Lock()
	for key, ss := range s.m {
		if ss.UserID == userID && key != k {
			delete(s.m, key)
			s.dirty = true
		}
	}
	s.mu.Unlock()
}

func (s *Sessions) Count() map[string]int {
	out := map[string]int{}
	s.mu.Lock()
	for _, ss := range s.m {
		out[ss.UserID]++
	}
	s.mu.Unlock()
	return out
}

func (s *Sessions) Save(dir string) error {
	if dir == "" {
		return nil
	}
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	out := make(map[string]Session, len(s.m))
	for k, ss := range s.m {
		c := *ss
		c.ID = ""
		out[k] = c
	}
	s.dirty = false
	s.mu.Unlock()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(out); err != nil {
		return err
	}
	return store.WriteFileAtomic(filepath.Join(dir, sessionsFile), buf.Bytes())
}

func (s *Sessions) Load(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, sessionsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var in map[string]Session
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&in); err != nil {
		return err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, ss := range in {
		if now.Sub(ss.LastSeen) > IdleTimeout || now.Sub(ss.Created) > MaxLifetime {
			continue
		}
		c := ss
		s.m[k] = &c
	}
	return nil
}
