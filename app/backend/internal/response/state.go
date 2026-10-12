package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS incident_response (
	alert_id   text PRIMARY KEY REFERENCES alerts (id) ON DELETE CASCADE,
	finished   boolean NOT NULL DEFAULT false,
	priority   text NOT NULL DEFAULT '',
	updated_at timestamptz NOT NULL,
	doc        jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS incident_response_open ON incident_response (alert_id) WHERE NOT finished;
`

// EnsureSchema creates the table of response states; the alert tables must exist.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schema+voiceSchema); err != nil {
		return fmt.Errorf("create the incident response table: %w", err)
	}
	return nil
}

// StepRun is an escalation step that was carried out.
type StepRun struct {
	Index   int       `json:"index"`
	At      time.Time `json:"at"`
	Reached []string  `json:"reached"`
	Failed  []string  `json:"failed,omitempty"`
	// People: names of the people the step called in.
	People []string `json:"people,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}

// Room is the war room chat of an incident.
type Room struct {
	ID  string `json:"id"`
	URL string `json:"url,omitempty"`
	// Members are the addresses (user principal names) in the chat.
	Members []string  `json:"members"`
	At      time.Time `json:"at"`
	DryRun  bool      `json:"dry_run,omitempty"`
}

// Bridge is the conference call of an incident.
type Bridge struct {
	Provider string    `json:"provider"`
	ID       string    `json:"id,omitempty"`
	URL      string    `json:"url,omitempty"`
	At       time.Time `json:"at"`
	DryRun   bool      `json:"dry_run,omitempty"`
}

// JiraIssue is an issue made for an incident.
type JiraIssue struct {
	Key    string    `json:"key"`
	URL    string    `json:"url,omitempty"`
	At     time.Time `json:"at"`
	DryRun bool      `json:"dry_run,omitempty"`
}

// Failure is an action that failed: it is tried again after Next until Attempts reach the limit.
type Failure struct {
	Attempts int       `json:"attempts"`
	Next     time.Time `json:"next"`
	Error    string    `json:"error"`
}

// State is what incident response did for an incident.
type State struct {
	AlertID    string     `json:"alert_id"`
	Assessment Assessment `json:"assessment"`
	// Priority is the response priority: the highest the assessment gave while the incident was
	// active (it is never lowered during an incident).
	Priority string `json:"priority"`
	// PDPriority is the priority last set on the PagerDuty incident.
	PDPriority string `json:"pd_priority,omitempty"`
	// OpenedAt is the opening of the incident the steps count from; a reopening starts the
	// steps again.
	OpenedAt   time.Time  `json:"opened_at"`
	Steps      []StepRun  `json:"steps"`
	Room       *Room      `json:"room,omitempty"`
	Bridge     *Bridge    `json:"bridge,omitempty"`
	Task       *JiraIssue `json:"task,omitempty"`
	Postmortem *JiraIssue `json:"postmortem,omitempty"`
	// Transitioned: the task was moved by the done transition.
	Transitioned bool `json:"transitioned,omitempty"`
	// Status is the incident status the war room and the task were last told about.
	Status string `json:"status"`
	// Failures by action (room, bridge, task, postmortem, transition…).
	Failures map[string]*Failure `json:"failures,omitempty"`
	// Skipped: actions skipped because their integration is off, recorded once.
	Skipped map[string]bool `json:"skipped,omitempty"`
	// Force: actions people asked for by hand (room, bridge, task, postmortem).
	Force    []string  `json:"force,omitempty"`
	Finished bool      `json:"finished"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

func (s *State) stepDone(i int) bool {
	for _, r := range s.Steps {
		if r.Index == i {
			return true
		}
	}
	return false
}

func loadState(ctx context.Context, db *pgxpool.Pool, id string) (*State, error) {
	var doc []byte
	err := db.QueryRow(ctx, "SELECT doc FROM incident_response WHERE alert_id = $1", id).Scan(&doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &State{}
	return st, json.Unmarshal(doc, st)
}

func saveState(ctx context.Context, db *pgxpool.Pool, st *State) error {
	doc, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO incident_response (alert_id, finished, priority, updated_at, doc) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (alert_id) DO UPDATE SET finished = $2, priority = $3, updated_at = $4, doc = $5`, st.AlertID, st.Finished, st.Priority, st.Updated, doc)
	return err
}
