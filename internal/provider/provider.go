// Package provider defines a cloud-agnostic credential backend abstraction.
//
// EZ Cloud Manager started as an AWS-only tool. This package decouples the CLI
// and UI from any single cloud: each backend (AWS today; Azure, GCP, Kubernetes
// contexts, generic .env later) implements Provider and registers itself, so new
// providers slot in without touching the command dispatch or the native UI.
package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ConnectionConflictMarker is a stable, machine-readable prefix emitted by
// the CLI when an optimistic connection save loses a race. Keep the marker
// independent of the human-readable text so native clients can classify the
// failure without coupling behavior to wording.
const ConnectionConflictMarker = "EZCLOUD_CONNECTION_CONFLICT"

// ErrConnectionConflict means a connection changed (or appeared/disappeared)
// after the caller loaded its editor baseline. Callers should refresh or
// explicitly merge instead of silently overwriting the newer value.
var ErrConnectionConflict = errors.New(ConnectionConflictMarker + ": connection changed since draft was loaded")

// ProfileSummary is a provider-agnostic profile listing entry.
type ProfileSummary struct {
	Name string   `json:"name"`
	Keys []string `json:"keys"`
	// Source distinguishes editable local credential records from external
	// vendor-managed session profiles. Empty means the provider's legacy
	// editable store; "sso" means authentication/config is vendor-owned.
	Source   string `json:"source,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	// Active marks the provider's own currently-active/default entry (e.g.
	// gcloud's active configuration). Only providers with a native "active"
	// concept that can BLOCK an operation (currently: gcp's delete-refuses-
	// if-active rule) set this; AWS/Azure leave it false (omitted), which is
	// the correct "not applicable" reading, not "definitely not active".
	Active bool `json:"active,omitempty"`
}

// Profile is a provider-agnostic set of credential fields for one profile.
type Profile struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

// Parsed is the result of parsing a pasted credentials/config blob.
type Parsed struct {
	ProfileName string            `json:"profileName,omitempty"`
	Fields      map[string]string `json:"fields"`
	// Notes surfaces parser decisions the user must know about, e.g.
	// "private_key ignored — keep service account keys in a file".
	Notes []string `json:"notes,omitempty"`
}

// FieldSpec describes one known profile field so the UI can render it without
// hardcoding provider knowledge: labels, secret masking, grouping, env-var
// export names and placeholder examples all come from the provider.
type FieldSpec struct {
	// Key is the storage key (e.g. "aws_secret_access_key", "core.project").
	Key string `json:"key"`
	// Display is the label shown in the UI (often the canonical env-var name).
	Display string `json:"display"`
	// Env is the environment variable this field exports to ("" = ini-only).
	Env string `json:"env,omitempty"`
	// Secret marks values that must be masked in UI, logs and diffs.
	Secret bool `json:"secret,omitempty"`
	// Common marks fields shown in the always-visible section of the editor.
	Common bool `json:"common,omitempty"`
	// Placeholder is a muted example value for empty fields.
	Placeholder string `json:"placeholder,omitempty"`
}

// Schema is a provider's full field catalog plus identity, in UI order.
type Schema struct {
	Provider    string      `json:"provider"`
	DisplayName string      `json:"displayName"`
	Fields      []FieldSpec `json:"fields"`
}

// SecretKeys returns the set of keys marked Secret, for redaction call sites.
func (s Schema) SecretKeys() map[string]bool {
	out := map[string]bool{}
	for _, f := range s.Fields {
		if f.Secret {
			out[f.Key] = true
		}
	}
	return out
}

// Provider abstracts a credential backend. Implementations own their storage
// location via DefaultPath and are responsible for safe, atomic persistence.
type Provider interface {
	// ID is the stable, lowercase identifier used for registration/lookup (e.g. "aws").
	ID() string
	// DisplayName is the human-facing label (e.g. "AWS").
	DisplayName() string
	// DefaultPath returns the backend's default storage location.
	DefaultPath() (string, error)
	// List returns the profiles found at path.
	List(path string) ([]ProfileSummary, error)
	// Get returns a single profile's fields (empty profile if not found).
	Get(path, name string) (Profile, error)
	// Save creates or updates a profile at path.
	Save(path, name string, fields map[string]string) error
	// Delete removes a profile at path (no-op if absent).
	Delete(path, name string) error
	// Parse extracts profile fields from a pasted credentials/config blob.
	Parse(text string) Parsed
	// Schema describes the provider's known fields for schema-driven UIs.
	Schema() Schema
}

// ConditionalSaver is an optional optimistic-concurrency capability. The
// implementation must compare and write while holding the same storage lock:
//
//   - expectAbsent=true succeeds only when name does not exist;
//   - otherwise the connection must exist and its normalized fields must
//     equal expectedFields.
//
// A failed precondition returns ErrConnectionConflict. This prevents a dirty
// editor or an SSO batch refresh from overwriting an independent update made
// after discovery.
type ConditionalSaver interface {
	SaveIfUnchanged(path, name string, fields, expectedFields map[string]string, expectAbsent bool) error
}

// Activator is an optional capability: providers that have a native notion of
// an "active"/default profile (gcloud configurations, the AWS default profile)
// implement it so the UI can offer a one-click switch.
type Activator interface {
	// Activate makes the named profile the provider's active/default one.
	Activate(path, name string) error
	// ActivateLabel is the human-facing action name (e.g. "Set as gcloud
	// active configuration").
	ActivateLabel() string
}

// Checker is an optional capability: providers that can verify a profile's
// credentials are actually live (a vendor identity call, e.g. `aws sts
// get-caller-identity`) implement it so the UI can offer "Test Connection".
// A provider not asserting Checker simply doesn't support it yet — the CLI
// and UI both treat that as a clean, expected outcome, not an error.
type Checker interface {
	// Check runs the provider's own identity/liveness call for the named
	// profile, scoped by ctx so a hung vendor CLI can be aborted. A failed
	// check (bad/expired credentials, no network) is reported via
	// CheckResult.OK/Error, NOT a returned error — err is reserved for
	// something this call itself couldn't attempt (e.g. an unreadable path).
	Check(ctx context.Context, path, name string) (CheckResult, error)
}

// CheckResult is the outcome of a Checker.Check call.
type CheckResult struct {
	OK bool `json:"ok"`
	// Identity is a small set of non-secret identity facts the vendor CLI
	// returned (e.g. AWS account/ARN) — present only when OK.
	Identity map[string]string `json:"identity,omitempty"`
	// Error is a human-readable failure reason — present only when !OK.
	Error string `json:"error,omitempty"`
}

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

// Register makes p available for lookup by its ID. It panics on an empty or
// duplicate ID, which can only be a programming error at init time.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	id := p.ID()
	if id == "" {
		panic("provider: Register called with empty ID")
	}
	if _, exists := registry[id]; exists {
		panic(fmt.Sprintf("provider: duplicate provider ID %q", id))
	}
	registry[id] = p
}

// Get returns the provider registered under id.
func Get(id string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (known: %v)", id, ids())
	}
	return p, nil
}

// IDs returns the sorted list of registered provider IDs.
func IDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	return ids()
}

// ids returns sorted registry keys; callers must hold mu.
func ids() []string {
	out := make([]string, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
