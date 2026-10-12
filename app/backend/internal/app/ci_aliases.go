package app

import (
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const maxCIAliases = 20

// AliasTakenError: an alias is a name another configuration item is already known by, so an
// event with that name would match neither of them.
type AliasTakenError struct{ Items []string }

func (e *AliasTakenError) Error() string { return strings.Join(e.Items, ", ") }

// normalAliases trims the aliases and drops the repeated ones (names match regardless of case).
func normalAliases(in []string) ([]string, error) {
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if utf8.RuneCountInString(v) > maxCIName || strings.ContainsFunc(v, unicode.IsControl) {
			return nil, invalid("invalid_ci_alias", nil)
		}
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, v) }) {
			out = append(out, v)
		}
	}
	if len(out) > maxCIAliases {
		return nil, invalid("too_many_aliases", nil)
	}
	return out, nil
}

// aliasConflicts names the other items known by one of the aliases (their ID, name, short name,
// addresses, aliases or DNS name).
func aliasConflicts(d *store.Data, id string, aliases []string) []string {
	want := map[string]bool{}
	for _, v := range aliases {
		want[strings.ToLower(v)] = true
	}
	var out []string
	for _, ci := range d.ConfigItems {
		if ci.ID == id {
			continue
		}
		keys := append([]string{ci.ID}, alert.CIKeys(ci)...)
		if slices.ContainsFunc(keys, func(k string) bool { return want[strings.ToLower(strings.TrimSpace(k))] }) {
			out = append(out, ci.Name)
		}
	}
	slices.Sort(out)
	return out
}

// SetAliases replaces the other names events call an item by. Aliases are kept in Umbrella, so
// items imported from NetBox take them too.
func (s *CIService) SetAliases(actor, id string, aliases []string) (CIView, error) {
	aliases, err := normalAliases(aliases)
	if err != nil {
		return CIView{}, err
	}
	var out CIView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		ci := d.ConfigItems[id]
		if ci == nil {
			return
		}
		if taken := aliasConflicts(d, id, aliases); len(taken) > 0 {
			err = &AliasTakenError{Items: taken}
			return
		}
		err = nil
		ci.Aliases, ci.UpdatedAt, ci.UpdatedBy = aliases, s.now(), actor
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "ci.aliases", Object: id, Detail: ci.Name + ": " + strings.Join(aliases, ", ")})
		out = ciView(d, ci)
	})
	return out, err
}

// AddAlias adds one alias unless the item is already known by that name.
func (s *CIService) AddAlias(actor, id, alias string) (CIView, error) {
	cur, err := s.current(id)
	if err != nil {
		return CIView{}, err
	}
	if knownAs(&cur, alias) {
		return s.Get(id)
	}
	return s.SetAliases(actor, id, append(slices.Clone(cur.Aliases), alias))
}

// knownAs reports whether an event naming the item this way finds it by its own names.
func knownAs(ci *model.ConfigItem, name string) bool {
	keys := append([]string{ci.ID}, alert.CIKeys(ci)...)
	for _, k := range alert.EventKeys(name) {
		if slices.ContainsFunc(keys, func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), k) }) {
			return true
		}
	}
	return false
}

type aliasesInput struct {
	Aliases []string `json:"aliases"`
}

func (a *App) setCIAliases(w http.ResponseWriter, r *http.Request) {
	var in aliasesInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.cis.SetAliases(current(r).user.Username, r.PathValue("id"), in.Aliases)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
