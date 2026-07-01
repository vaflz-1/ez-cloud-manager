// Package provider defines a cloud-agnostic credential backend abstraction.
//
// EZ Cloud Manager started as an AWS-only tool. This package decouples the CLI
// and UI from any single cloud: each backend (AWS today; Azure, GCP, Kubernetes
// contexts, generic .env later) implements Provider and registers itself, so new
// providers slot in without touching the command dispatch or the native UI.
package provider

import (
	"fmt"
	"sort"
	"sync"
)

// ProfileSummary is a provider-agnostic profile listing entry.
type ProfileSummary struct {
	Name string   `json:"name"`
	Keys []string `json:"keys"`
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
