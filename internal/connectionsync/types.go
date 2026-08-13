// Package connectionsync implements the user-initiated, vendor-CLI-backed
// discovery and synchronization flow for external cloud identities.
//
// Security boundary: this package never reads AWS SSO or gcloud token caches,
// never returns tokens/device codes in its JSON DTOs, and never invokes a
// shell. Authentication remains owned by the official vendor CLIs.
package connectionsync

import "errors"

const ProtocolVersion = 1

const (
	StatusNew       = "new"
	StatusUpdate    = "update"
	StatusUnchanged = "unchanged"

	ModeSelected  = "selected"
	ModeUpdateAll = "update-all"
	ModeAddNew    = "add-new"
)

var ErrSnapshotChanged = errors.New("connection auth snapshot changed; discover again")

// Candidate is deliberately secret-free. Provider-specific fields are
// omitted when irrelevant, keeping one stable wire contract for the native UI.
type Candidate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"displayName"`
	SourceProfile string `json:"sourceProfile,omitempty"`
	AuthMode      string `json:"authMode"`
	Principal     string `json:"principal,omitempty"`
	AccountID     string `json:"accountID,omitempty"`
	RoleName      string `json:"roleName,omitempty"`
	ProjectID     string `json:"projectID,omitempty"`
	Region        string `json:"region,omitempty"`
	Status        string `json:"status"`
	CanApply      bool   `json:"canApply"`
	Reason        string `json:"reason,omitempty"`
}

type DiscoverySnapshot struct {
	ProtocolVersion int         `json:"protocolVersion"`
	Provider        string      `json:"provider"`
	Principal       string      `json:"principal,omitempty"`
	Revision        string      `json:"revision"`
	Candidates      []Candidate `json:"candidates"`
	Warnings        []string    `json:"warnings"`
}

type LoginRequest struct {
	ExpectedRevision string   `json:"expectedRevision,omitempty"`
	CandidateIDs     []string `json:"candidateIDs,omitempty"`
}

type LoginResponse struct {
	Provider string            `json:"provider"`
	OK       bool              `json:"ok"`
	LoggedIn int               `json:"loggedIn"`
	Snapshot DiscoverySnapshot `json:"snapshot"`
}

type ApplyRequest struct {
	ExpectedRevision string   `json:"expectedRevision"`
	Principal        string   `json:"principal,omitempty"`
	Mode             string   `json:"mode"`
	CandidateIDs     []string `json:"candidateIDs,omitempty"`
}

type ApplyResult struct {
	CandidateID string `json:"candidateID"`
	Name        string `json:"name"`
	Action      string `json:"action"`
}

type ApplyResponse struct {
	Provider  string        `json:"provider"`
	Revision  string        `json:"revision"`
	Results   []ApplyResult `json:"results"`
	Added     int           `json:"added"`
	Updated   int           `json:"updated"`
	Unchanged int           `json:"unchanged"`
}
