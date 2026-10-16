package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/ledger"
	"github.com/sbrw-evc/beacon/app/backend/internal/ledger/ledgertest"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/secrets"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

type mapSecrets map[string]string

func (m mapSecrets) Resolve(ref string) (string, error) {
	if v, ok := m[ref]; ok {
		return v, nil
	}
	return "", errors.New("no secret")
}
func (m mapSecrets) PutRef(_ context.Context, path, key, value string) (string, error) {
	m[path+"#"+key] = value
	return path + "#" + key, nil
}
func (m mapSecrets) Status(context.Context) secrets.Status { return secrets.Status{} }

func TestInventoryDBPushSendsChangesAndResolves(t *testing.T) {
	srv := ledgertest.Start(t)
	st := store.New()
	sec := mapSecrets{"tok": ledgertest.Token, "sec": ledgertest.Secret}
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.PublicURL = "https://umbrella.example/"
		d.Settings.InventoryDB = ledger.Config{Enabled: true, URL: srv.URL, IntegrationID: ledgertest.Integration,
			TokenRef: "tok", SecretRef: "sec", PushStatus: true, ImportDevices: true}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "srv-db-01", Source: model.SourceInventoryDB,
			InventoryDB: &model.InventoryRef{ID: 1, Ref: ledger.Ref(1)}}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "local-only", Source: model.SourceLocal}
	})
	active := []alert.Alert{
		{ID: "A1", CIID: "CI-1", Status: alert.StatusOpen, Severity: model.SeverityLow, Title: "Disk 85%", Signal: "use.disk"},
		{ID: "A2", CIID: "CI-2", Status: alert.StatusOpen, Severity: model.SeverityCritical, Title: "not in Ledger"},
	}
	s := NewInventoryDBService(st, sec, func(context.Context) ([]alert.Alert, error) { return active, nil })
	ctx := context.Background()

	if r, err := s.Push(ctx); err != nil || r.Sent != 1 {
		t.Fatalf("first push = %+v %v", r, err)
	}
	ev := srv.Events()
	if len(ev) != 1 || ev[0].AlertID != "A1" || ev[0].Severity != model.SeverityInfo || ev[0].CI.SourceRefs[0] != "inventory-db:dcim.device:1" ||
		ev[0].Links.Incident != "https://umbrella.example/incidents?id=A1" || ev[0].Signal != "use.disk" {
		t.Fatalf("events = %+v", ev)
	}
	// Nothing changed: nothing is sent.
	if r, _ := s.Push(ctx); r.Sent+r.Resolved != 0 || len(srv.Events()) != 1 {
		t.Fatalf("unchanged push sent %+v", r)
	}
	// Acknowledged: sent again.
	active[0].Status = alert.StatusAcknowledged
	if r, _ := s.Push(ctx); r.Sent != 1 || srv.Events()[1].Status != alert.StatusAcknowledged {
		t.Fatalf("ack push = %+v %+v", r, srv.Events())
	}
	// No longer active: reported resolved once.
	active = active[1:]
	if r, _ := s.Push(ctx); r.Resolved != 1 || srv.Events()[2].Status != alert.StatusResolved || srv.Events()[2].AlertID != "A1" {
		t.Fatalf("resolve push = %+v %+v", r, srv.Events())
	}
	if r, _ := s.Push(ctx); r.Resolved != 0 || len(srv.Events()) != 3 {
		t.Fatalf("resolved twice: %+v", r)
	}
	st.Read(func(d *store.Data) {
		if len(d.InventoryDBPushed) != 0 {
			t.Fatalf("pushed = %v", d.InventoryDBPushed)
		}
	})
}

func TestInventoryDBPushRetriesAfterAFailure(t *testing.T) {
	srv := ledgertest.Start(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.InventoryDB = ledger.Config{Enabled: true, URL: srv.URL, IntegrationID: ledgertest.Integration,
			TokenRef: "tok", SecretRef: "sec", PushStatus: true}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "a", Source: model.SourceInventoryDB, InventoryDB: &model.InventoryRef{ID: 1, Ref: ledger.Ref(1)}}
	})
	// A wrong secret: Ledger refuses, nothing is recorded as sent.
	sec := mapSecrets{"tok": ledgertest.Token, "sec": "whsec_wrong"}
	active := []alert.Alert{{ID: "A1", CIID: "CI-1", Status: alert.StatusOpen, Severity: model.SeverityError, Title: "x", LastSeen: time.Now()}}
	s := NewInventoryDBService(st, sec, func(context.Context) ([]alert.Alert, error) { return active, nil })
	if _, err := s.Push(context.Background()); err == nil {
		t.Fatal("push with a wrong secret succeeded")
	}
	sec["sec"] = ledgertest.Secret
	if r, err := s.Push(context.Background()); err != nil || r.Sent != 1 {
		t.Fatalf("retry = %+v %v", r, err)
	}
}

func TestInventoryDBPushIsOffUnlessTurnedOn(t *testing.T) {
	st := store.New()
	s := NewInventoryDBService(st, mapSecrets{}, func(context.Context) ([]alert.Alert, error) { return nil, nil })
	if _, err := s.Push(context.Background()); !errors.Is(err, ErrInventoryOff) {
		t.Fatalf("push while off = %v", err)
	}
}
