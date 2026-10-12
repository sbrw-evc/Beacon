package store_test

import (
	"context"
	"slices"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type memBackend struct{ data []byte }

func (m *memBackend) Kind() string                              { return "memory" }
func (m *memBackend) Where() string                             { return "test" }
func (m *memBackend) Load(context.Context) ([][]byte, error)    { return [][]byte{m.data}, nil }
func (m *memBackend) Save(_ context.Context, data []byte) error { m.data = data; return nil }
func (m *memBackend) Close()                                    {}

// A snapshot of a version with one team per user loads with that team in the team list.
func TestSnapshotMovesSingleTeamIntoTeamList(t *testing.T) {
	old := store.New()
	old.Write(func(d *store.Data) {
		d.Users["U-1"] = &model.User{ID: "U-1", Username: "anna", TeamID: "T-1", MappedTeam: "T-1"}
		d.Users["U-2"] = &model.User{ID: "U-2", Username: "boris"}
		d.Users["U-3"] = &model.User{ID: "U-3", Username: "vera", TeamID: "T-2", TeamIDs: []string{"T-2", "T-3"}}
	})
	data, err := old.Export()
	if err != nil {
		t.Fatal(err)
	}
	st := store.New()
	if ok, err := st.Attach(context.Background(), &memBackend{data: data}); !ok || err != nil {
		t.Fatalf("attach = %v %v", ok, err)
	}
	st.Read(func(d *store.Data) {
		if u := d.Users["U-1"]; !slices.Equal(u.TeamIDs, []string{"T-1"}) || !slices.Equal(u.MappedTeams, []string{"T-1"}) || u.TeamID != "" || u.MappedTeam != "" {
			t.Errorf("anna = %+v", u)
		}
		if u := d.Users["U-2"]; len(u.TeamIDs) != 0 {
			t.Errorf("boris = %+v", u)
		}
		if u := d.Users["U-3"]; !slices.Equal(u.TeamIDs, []string{"T-2", "T-3"}) {
			t.Errorf("vera = %+v", u.TeamIDs)
		}
	})
}
