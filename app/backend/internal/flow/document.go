package flow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const (
	DocumentFormat  = "umbrella.connector"
	DocumentVersion = 1
)

// Document is the export format of a connector, also used for the built-in presets. It never
// contains secrets: credentials are replaced by slots that the import maps to credentials of
// the target installation.
type Document struct {
	Format        string           `json:"format"`
	FormatVersion int              `json:"format_version"`
	Name          string           `json:"name"`
	Description   string           `json:"description,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	Graph         Graph            `json:"graph"`
	Credentials   []CredentialSlot `json:"credentials"`
	Samples       []DocSample      `json:"samples,omitempty"`
}

type CredentialSlot struct {
	Slot  string   `json:"slot"`
	Node  string   `json:"node"`
	Param string   `json:"param"`
	Types []string `json:"types"`
	Name  string   `json:"name,omitempty"`
}

type DocSample struct {
	Name    string            `json:"name"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Export turns a graph into a document graph: every credential parameter becomes a slot.
// name returns a credential's display name, kept as a hint for the import.
func Export(g Graph, name func(id string) string) (Graph, []CredentialSlot) {
	var out Graph
	_ = json.Unmarshal(g.JSON(), &out)
	slots := []CredentialSlot{}
	for i, n := range out.Nodes {
		t, ok := TypeOf(n.Type, n.TypeVersion)
		if !ok {
			continue
		}
		for _, p := range t.Params {
			if p.Kind != KindCredential {
				continue
			}
			id, _ := n.Params[p.Key].(string)
			if id == "" {
				continue
			}
			slot := CredentialSlot{Slot: n.ID + "." + p.Key, Node: n.ID, Param: p.Key, Types: slices.Clone(p.CredentialTypes)}
			if name != nil {
				slot.Name = name(id)
			}
			slots = append(slots, slot)
			out.Nodes[i].Params[p.Key] = ""
		}
	}
	return out, slots
}

func ParseDocument(b []byte) (Document, error) {
	var d Document
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("the file is not a connector export: %w", err)
	}
	if d.Format != DocumentFormat {
		return d, fmt.Errorf("the file is not a connector export (format %q)", d.Format)
	}
	if d.FormatVersion < 1 || d.FormatVersion > DocumentVersion {
		return d, fmt.Errorf("export format version %d is not supported", d.FormatVersion)
	}
	for _, n := range d.Graph.Nodes {
		if _, ok := TypeOf(n.Type, n.TypeVersion); !ok {
			return d, fmt.Errorf("node %s has unknown type %s v%d", n.ID, n.Type, n.TypeVersion)
		}
	}
	return d, nil
}

// Apply fills the credential slots from mapping (slot → credential ID) and returns the graph
// for a new draft. Unmapped slots stay empty and show up as issues in the editor.
func (d Document) Apply(mapping map[string]string, typeOf func(id string) (string, bool)) (Graph, error) {
	var g Graph
	_ = json.Unmarshal(d.Graph.JSON(), &g)
	var errs []error
	for _, s := range d.Credentials {
		id := mapping[s.Slot]
		if id == "" {
			continue
		}
		typ, ok := typeOf(id)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: credential %s does not exist", s.Slot, id))
			continue
		case len(s.Types) > 0 && !slices.Contains(s.Types, typ):
			errs = append(errs, fmt.Errorf("%s: a %s credential cannot be used here", s.Slot, typ))
			continue
		}
		for i := range g.Nodes {
			if g.Nodes[i].ID == s.Node {
				if g.Nodes[i].Params == nil {
					g.Nodes[i].Params = map[string]any{}
				}
				g.Nodes[i].Params[s.Param] = id
			}
		}
	}
	for slot := range mapping {
		if !slices.ContainsFunc(d.Credentials, func(s CredentialSlot) bool { return s.Slot == slot }) {
			errs = append(errs, fmt.Errorf("the export has no credential slot %s", slot))
		}
	}
	return g, errors.Join(errs...)
}
