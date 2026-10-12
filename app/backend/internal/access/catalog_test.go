package access

import (
	"slices"
	"testing"
)

func TestNormalizeAddsViewAndDropsUnknown(t *testing.T) {
	got := Normalize([]string{"users:lock", "nope:view", "users:lock", " roles:edit "})
	want := []string{"users:view", "users:lock", "roles:view", "roles:edit"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEveryPageHasViewAndUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Pages() {
		if seen[p.ID] {
			t.Fatalf("duplicate page %s", p.ID)
		}
		seen[p.ID] = true
		if len(p.Features) == 0 || p.Features[0].ID != View {
			t.Fatalf("page %s must start with the view feature", p.ID)
		}
		if !slices.ContainsFunc(Groups(), func(g Group) bool { return g.ID == p.Group }) {
			t.Fatalf("page %s has unknown group %s", p.ID, p.Group)
		}
	}
}
