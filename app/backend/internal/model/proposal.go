package model

import "time"

const (
	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"

	// ProposalDependsOn is the one proposal type: the service of From depends on the service of To.
	ProposalDependsOn = "depends_on"

	// ProposalTTL: a proposal its source has not refreshed for this long expires.
	ProposalTTL = 14 * 24 * time.Hour
	// ProposalRegrowth: a rejected proposal comes back when its evidence grows this many times.
	ProposalRegrowth = 10
)

// ProposalEnd is one side of a proposed dependency as the source names it, and the
// configuration item Beacon resolved it to.
type ProposalEnd struct {
	SourceRef string `json:"source_ref,omitempty"`
	Name      string `json:"name,omitempty"`
	IP        string `json:"ip,omitempty"`
	CIID      string `json:"ci_id"`
}

// ProposalEvidence is what the source saw: the traffic between the two items.
type ProposalEvidence struct {
	Protocol  string `json:"protocol,omitempty"`
	Port      int    `json:"port,omitempty"`
	App       string `json:"app,omitempty"`
	Bytes7d   int64  `json:"bytes_7d"`
	Flows7d   int64  `json:"flows_7d"`
	DaysSeen  int    `json:"days_seen"`
	FirstSeen string `json:"first_seen,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
	FlowURL   string `json:"flow_url,omitempty"`
}

// DependencyProposal is a dependency between business services a source (Current)
// derived from steady traffic between their configuration items. People accept or reject it
// on the CMDB map.
type DependencyProposal struct {
	Key         string           `json:"key"`
	Source      string           `json:"source"`
	Type        string           `json:"type"`
	From        ProposalEnd      `json:"from"`
	To          ProposalEnd      `json:"to"`
	Evidence    ProposalEvidence `json:"evidence"`
	Status      string           `json:"status"`
	GeneratedAt time.Time        `json:"generated_at,omitzero"`
	CreatedAt   time.Time        `json:"created_at"`
	RefreshedAt time.Time        `json:"refreshed_at"`
	DecidedAt   *time.Time       `json:"decided_at,omitempty"`
	DecidedBy   string           `json:"decided_by,omitempty"`
	// RejectedBytes and RejectedFlows are the evidence when the proposal was rejected; it stays
	// hidden until the evidence grows ProposalRegrowth times.
	RejectedBytes int64 `json:"-"`
	RejectedFlows int64 `json:"-"`
}
