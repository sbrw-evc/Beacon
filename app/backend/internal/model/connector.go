package model

import "time"

// Connector definitions live in the state snapshot with the rest of the configuration, so they
// move with it when the storage is switched. Received requests, events and failures are
// high-volume data and live in their own PostgreSQL tables (package ingest).
type Connector struct {
	ID           string
	Slug         string
	Name         string
	Description  string
	Tags         []string
	Preset       string
	CreatedAt    time.Time
	CreatedBy    string
	UpdatedAt    time.Time
	Draft        ConnectorDraft
	Published    int
	PublishedAt  *time.Time
	PublishedBy  string
	PublishError string
	Versions     []ConnectorVersion
	Samples      []Sample
	Capture      *Capture
	Lock         *EditLock
}

// ConnectorDraft is the one mutable copy of a connector's graph, saved by the editor.
type ConnectorDraft struct {
	Graph     []byte
	Pins      []byte
	Revision  int
	UpdatedAt time.Time
	UpdatedBy string
}

// ConnectorVersion is an immutable published snapshot. Requests are processed by the version
// that was published when they arrived.
type ConnectorVersion struct {
	Number    int
	Graph     []byte
	Hash      string
	Name      string
	Comment   string
	CreatedAt time.Time
	CreatedBy string
}

type Sample struct {
	ID        string
	Name      string
	Source    string
	Body      []byte
	Headers   map[string]string
	Query     map[string]string
	RemoteIP  string
	Method    string
	CreatedAt time.Time
	CreatedBy string
}

// Capture makes the next requests to the connector's address also become samples.
type Capture struct {
	Remaining int
	Until     time.Time
	By        string
}

type EditLock struct {
	UserID   string
	Username string
	Name     string
	Until    time.Time
}

// Credential is how Umbrella checks or presents a secret. Secret values live only in OpenBao;
// Secrets maps a field to its openbao:// reference.
type Credential struct {
	ID          string
	Name        string
	Type        string
	Description string
	Fields      map[string]string
	Secrets     map[string]string
	Version     int
	CreatedAt   time.Time
	CreatedBy   string
	UpdatedAt   time.Time
	UpdatedBy   string
}
