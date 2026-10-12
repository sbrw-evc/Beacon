package store

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const snapshotFormat = 2

var ErrNoState = errors.New("no saved state")

type Backend interface {
	Kind() string
	Where() string
	Load(ctx context.Context) ([][]byte, error)
	Save(ctx context.Context, data []byte) error
	Close()
}

type snapshot struct {
	Format  int
	SavedAt time.Time
	Data    Data
}

func decode(b []byte) (snapshot, error) {
	var snap snapshot
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&snap); err != nil {
		return snap, err
	}
	if snap.Format != snapshotFormat {
		return snap, fmt.Errorf("unsupported state format %d", snap.Format)
	}
	return snap, nil
}

func (s *Store) Attach(ctx context.Context, b Backend) (bool, error) {
	cands, err := b.Load(ctx)
	if errors.Is(err, ErrNoState) {
		s.persistMu.Lock()
		s.backend = b
		s.persistMu.Unlock()
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var lastErr error
	for i, c := range cands {
		snap, err := decode(c)
		if err != nil {
			lastErr = err
			slog.Error("snapshot is unreadable", "backend", b.Kind(), "where", b.Where(), "candidate", i, "err", err)
			continue
		}
		s.install(snap)
		s.persistMu.Lock()
		s.backend = b
		s.persistMu.Unlock()
		slog.Info("state restored", "backend", b.Kind(), "where", b.Where(), "saved_at", snap.SavedAt, "users", len(snap.Data.Users))
		return true, nil
	}
	return false, fmt.Errorf("saved state is unreadable, refusing to overwrite it: %w", lastErr)
}

func (s *Store) install(snap snapshot) {
	snap.Data.init()
	s.mu.Lock()
	s.d = snap.Data
	s.version++
	v := s.version
	s.mu.Unlock()
	s.persistMu.Lock()
	s.saved = v
	s.persistMu.Unlock()
}

func (s *Store) Persistent() bool {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return s.backend != nil
}

type PersistStatus struct {
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind,omitempty"`
	Where   string `json:"where,omitempty"`
	Pending bool   `json:"pending"`
	Error   string `json:"error,omitempty"`
}

func (s *Store) PersistStatus() PersistStatus {
	s.persistMu.Lock()
	b, saved, err := s.backend, s.saved, s.lastErr
	s.persistMu.Unlock()
	s.mu.RLock()
	v := s.version
	s.mu.RUnlock()
	st := PersistStatus{Enabled: b != nil, Pending: b != nil && v != saved}
	if b != nil {
		st.Kind, st.Where = b.Kind(), b.Where()
	}
	if err != nil {
		st.Error = err.Error()
	}
	return st
}

func (s *Store) Flush() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if s.backend == nil {
		return nil
	}
	var buf bytes.Buffer
	s.mu.RLock()
	v := s.version
	if v == s.saved {
		s.mu.RUnlock()
		return nil
	}
	err := gob.NewEncoder(&buf).Encode(snapshot{Format: snapshotFormat, SavedAt: time.Now(), Data: s.d})
	s.mu.RUnlock()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = s.backend.Save(ctx, buf.Bytes())
		cancel()
	}
	s.lastErr = err
	if err != nil {
		return err
	}
	s.saved = v
	return nil
}

func (s *Store) Export() ([]byte, error) {
	var buf bytes.Buffer
	s.mu.RLock()
	err := gob.NewEncoder(&buf).Encode(snapshot{Format: snapshotFormat, SavedAt: time.Now(), Data: s.d})
	s.mu.RUnlock()
	return buf.Bytes(), err
}

func WriteFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".bak"); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (s *Store) Run(ctx context.Context, every time.Duration, extra ...func() error) {
	tk := time.NewTicker(every)
	defer tk.Stop()
	flush := func() {
		if err := s.Flush(); err != nil {
			slog.Error("state snapshot failed", "err", err)
		}
		for _, f := range extra {
			if err := f(); err != nil {
				slog.Error("snapshot failed", "err", err)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-tk.C:
			flush()
		}
	}
}
