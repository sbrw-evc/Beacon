package model

// Field is a named value a connector shows in an incident: a link to the dashboard, the rule,
// the query.
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
