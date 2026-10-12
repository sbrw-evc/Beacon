package inventorydb_test

import (
	"context"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/inventorydb"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/inventorydb/inventorydbtest"
)

func cfg(url string) inventorydb.Config {
	c := inventorydb.Defaults()
	c.Enabled, c.URL, c.IntegrationID = true, url, inventorydbtest.Integration
	return c
}

func TestNormalize(t *testing.T) {
	c, err := cfg("https://inv.example/api/v1/").Normalize()
	if err != nil || c.URL != "https://inv.example" {
		t.Fatalf("normalize = %q %v", c.URL, err)
	}
	for _, bad := range []inventorydb.Config{cfg("ftp://x"), cfg("https://u:p@x"), {URL: "https://x", IntegrationID: "a/b"}, {URL: "https://x", IntegrationID: ""}} {
		if _, err := bad.Normalize(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestFeedPagesAndTest(t *testing.T) {
	srv := inventorydbtest.Start(t)
	var items []map[string]any
	for i := 1; i <= 2500; i++ {
		items = append(items, inventorydbtest.Device(i, "srv", "10.0.0.1"))
	}
	srv.Set(items...)
	c, err := inventorydb.New(cfg(srv.URL), inventorydbtest.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	feed, err := c.Feed(context.Background())
	if err != nil || len(feed) != 2500 || feed[2499].ID() != 2500 {
		t.Fatalf("feed = %d %v", len(feed), err)
	}
	if feed[0].Attr("site") != "DC1" || feed[0].Tags()[0] != "prod" || feed[0].Identities.Serial != "SN1" {
		t.Fatalf("item = %+v", feed[0])
	}
	if p, err := c.Test(context.Background()); err != nil || p.Items != 2500 {
		t.Fatalf("test = %+v %v", p, err)
	}
	bad, _ := inventorydb.New(cfg(srv.URL), "wrong", "")
	if _, err := bad.Test(context.Background()); err == nil || err.Error() != "Inventory DB answered 401: Authentication required" {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestPushSigns(t *testing.T) {
	srv := inventorydbtest.Start(t)
	c, _ := inventorydb.New(cfg(srv.URL), inventorydbtest.Token, inventorydbtest.Secret)
	events := make([]inventorydb.AlertEvent, 450)
	for i := range events {
		events[i] = inventorydb.AlertEvent{AlertID: "a", Status: "open", Severity: "error", Title: "x", CI: inventorydb.AlertCI{SourceRefs: []string{inventorydb.Ref(1)}}}
	}
	if err := c.Push(context.Background(), events); err != nil || len(srv.Events()) != 450 {
		t.Fatalf("push = %v, %d received", err, len(srv.Events()))
	}
	wrong, _ := inventorydb.New(cfg(srv.URL), inventorydbtest.Token, "whsec_other")
	if err := wrong.Push(context.Background(), events[:1]); err == nil {
		t.Fatal("a wrong secret was accepted")
	}
	none, _ := inventorydb.New(cfg(srv.URL), inventorydbtest.Token, "")
	if err := none.Push(context.Background(), events[:1]); err == nil {
		t.Fatal("pushed without a secret")
	}
}

func TestSignMatchesInventoryDB(t *testing.T) {
	// HMAC-SHA256("s1", "1700000000.{}") as Inventory DB's Node code computes it.
	const want = "v1=3090f954c37938e70824aaf9a208b3d0826ce15ec4a840385d62ca283b91f73a"
	if got := inventorydb.Sign("s1", "1700000000", []byte("{}")); got != want {
		t.Fatalf("sign = %s, want %s", got, want)
	}
}
