// Package profile implements the v2.0 global profile engine described in
// docs/PLATFORM.md: a profile is a named bundle of cloud-account references,
// env vars, enabled plugins and settings overrides that one app window binds
// to. It is the promoted successor to internal/workspace (kept, unmodified,
// as the read-only source for MigrateFromWorkspaces).
//
// Terminology: "profile" here means this new global container, NOT the
// existing per-provider credential entry (provider.Profile, --profile NAME).
// A profile references those credential entries by name via AccountRef —
// {Provider, Account} — so "account" always means a credential entry and
// "profile" always means this container. See cmd/ezcloud/profile.go for why
// its dispatcher is named profileMgmtCommand rather than profileCommand.
//
// SECURITY: like internal/workspace, a profile never stores credential
// material. EnvVars are meant for non-secret configuration (e.g. a default
// region); Save/Create hard-reject any env var key that looks like a secret
// (see looksLikeSecret) rather than merely warning about it.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ez-cloud-manager/internal/plugin"
)

const (
	// currentVersion is the on-disk schema version this build writes. Unlike
	// internal/workspace's single envelope version, each profile.json carries
	// its own version — List skips (rather than fails on) any one profile
	// that a newer build wrote.
	//
	// v2: P1 plugin host — EnabledPlugins gains real meaning; see
	// readProfile's legacy-profile migration below.
	currentVersion = 2
	// maxNameLen bounds a profile name, measured in Unicode code points.
	maxNameLen = 64
	// maxEnvVars caps env vars per profile — generous for real use, small
	// enough that a profile.json can never balloon.
	maxEnvVars = 200
	// maxEnabledPlugins caps enabled-plugin ids per profile — same rationale
	// as maxEnvVars.
	maxEnabledPlugins = 50
)

// AccountRef references one (provider, account) credential entry by name —
// no secrets, just like internal/workspace.Member before it.
type AccountRef struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
}

// EnvVar is one non-secret environment variable a profile applies to the
// `ezcloud` CLI child process for every account operation run in its window.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Profile is a global profile: the unit one app window binds to.
type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ShowAllAccounts disables the Accounts filter (the window shows every
	// account across every provider instead of only the referenced ones).
	ShowAllAccounts bool         `json:"showAllAccounts"`
	Accounts        []AccountRef `json:"accounts"`
	EnvVars         []EnvVar     `json:"envVars"`
	// EnabledPlugins is a P1 placeholder — always empty until the plugin
	// host (PLATFORM.md phase P1) exists.
	EnabledPlugins []string `json:"enabledPlugins"`
	// Settings is a P1+ overrides placeholder, unused by any UI in P0.
	Settings map[string]string `json:"settings,omitempty"`
	// WindowState is opaque to Go — the Swift UI owns its shape (P0 does not
	// populate or read it yet; it exists so P1 needs no data migration).
	WindowState json.RawMessage `json:"windowState,omitempty"`
	Version     int             `json:"version"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

// DefaultRoot returns the profiles directory: one folder per profile, each
// holding profile.json. EZCLOUD_DATA_DIR overrides the EZCloudManager data
// root (distinct from EZCLOUD_CONFIG_DIR, which stays scoped to audit.log
// and the legacy workspaces.json); otherwise it falls back to
// ~/Library/Application Support/EZCloudManager.
func DefaultRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv("EZCLOUD_DATA_DIR")); override != "" {
		return filepath.Join(override, "profiles"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "EZCloudManager", "profiles"), nil
}

// List returns all profiles sorted by name (case-insensitive). A missing
// root is "no profiles yet", not an error. A profile folder that is
// unreadable, corrupt, has a schema version newer than this build
// understands, or whose profile.json ID doesn't match its folder name is
// skipped silently — one bad entry never hides the rest of the list.
func List(root string) ([]Profile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Profile{}, nil
		}
		return nil, err
	}

	list := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		p, err := readProfile(root, entry.Name())
		if err != nil {
			continue
		}
		list = append(list, p)
	}
	sortByName(list)
	return list, nil
}

// Get returns one profile by ID.
func Get(root, id string) (Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Profile{}, errors.New("profile id is required")
	}
	p, err := readProfile(root, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Profile{}, fmt.Errorf("profile %q not found", id)
		}
		return Profile{}, err
	}
	return p, nil
}

// Create validates p, assigns it a fresh ID and timestamps, and writes it to
// a new folder under root. The name must not collide (case-insensitively)
// with an existing profile.
func Create(root string, p Profile) (Profile, error) {
	normalized, err := validateProfile(p)
	if err != nil {
		return Profile{}, err
	}
	existing, err := List(root)
	if err != nil {
		return Profile{}, err
	}
	if nameTaken(existing, normalized.Name, "") {
		return Profile{}, fmt.Errorf("profile %q already exists", normalized.Name)
	}

	id, err := generateID()
	if err != nil {
		return Profile{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	normalized.ID = id
	normalized.Version = currentVersion
	normalized.CreatedAt = now
	normalized.UpdatedAt = now
	if err := writeProfile(root, normalized); err != nil {
		return Profile{}, err
	}
	return normalized, nil
}

// Save re-validates and rewrites an existing profile in place, identified by
// p.ID (which must already exist). The name may change but still must not
// collide with a DIFFERENT profile. CreatedAt is preserved; UpdatedAt is
// refreshed.
func Save(root string, p Profile) error {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return errors.New("profile id is required")
	}
	current, err := Get(root, id)
	if err != nil {
		return err
	}
	normalized, err := validateProfile(p)
	if err != nil {
		return err
	}
	list, err := List(root)
	if err != nil {
		return err
	}
	if nameTaken(list, normalized.Name, id) {
		return fmt.Errorf("profile %q already exists", normalized.Name)
	}

	normalized.ID = id
	normalized.Version = currentVersion
	normalized.CreatedAt = current.CreatedAt
	normalized.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return writeProfile(root, normalized)
}

// Rename changes a profile's name, rejecting a collision with a different
// profile. Renaming to the current name is allowed (a no-op rewrite).
func Rename(root, id, newName string) error {
	p, err := Get(root, id)
	if err != nil {
		return err
	}
	newName = strings.TrimSpace(newName)
	if err := validateName(newName); err != nil {
		return err
	}
	list, err := List(root)
	if err != nil {
		return err
	}
	if nameTaken(list, newName, id) {
		return fmt.Errorf("profile %q already exists", newName)
	}

	p.Name = newName
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return writeProfile(root, p)
}

// Duplicate copies an existing profile's Accounts/EnvVars/Settings under a
// fresh ID. An empty newName defaults to "<original> copy", deduped against
// existing names; an explicit newName that collides is rejected.
func Duplicate(root, id, newName string) (Profile, error) {
	src, err := Get(root, id)
	if err != nil {
		return Profile{}, err
	}
	list, err := List(root)
	if err != nil {
		return Profile{}, err
	}

	name := strings.TrimSpace(newName)
	if name == "" {
		name = uniqueName(list, src.Name+" copy", "")
	} else if nameTaken(list, name, "") {
		return Profile{}, fmt.Errorf("profile %q already exists", name)
	}

	return Create(root, Profile{
		Name:            name,
		ShowAllAccounts: src.ShowAllAccounts,
		Accounts:        src.Accounts,
		EnvVars:         src.EnvVars,
		EnabledPlugins:  src.EnabledPlugins,
		Settings:        src.Settings,
	})
}

// Delete removes a profile folder. The last remaining profile can never be
// deleted: every window must bind to exactly one profile, so the app can
// never be left with zero.
func Delete(root, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("profile id is required")
	}
	list, err := List(root)
	if err != nil {
		return err
	}

	found := false
	for _, p := range list {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("profile %q not found", id)
	}
	if len(list) <= 1 {
		return errors.New("cannot delete the last remaining profile")
	}
	return os.RemoveAll(filepath.Join(root, id))
}

// readProfile loads and validates one profile folder's contents.
func readProfile(root, dirName string) (Profile, error) {
	path := filepath.Join(root, dirName, "profile.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}

	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.Version > currentVersion {
		return Profile{}, fmt.Errorf("profile %s was created by a newer version of ez-cloud-manager (schema version %d; this build understands up to %d)", path, p.Version, currentVersion)
	}
	if p.ID != dirName {
		return Profile{}, fmt.Errorf("profile %s: id %q does not match its folder name %q", path, p.ID, dirName)
	}
	if p.Accounts == nil {
		p.Accounts = []AccountRef{}
	}
	if p.EnvVars == nil {
		p.EnvVars = []EnvVar{}
	}
	if p.EnabledPlugins == nil {
		p.EnabledPlugins = []string{}
	}
	// legacyDefaultPlugin is auto-enabled (in memory, on every read) for a
	// profile written by a pre-P1 build (Version < 2) — so an existing
	// user's window keeps showing what it always showed (the credentials
	// browser) instead of a suddenly-empty hub. This is intentionally NOT a
	// batch migration pass: it becomes PERMANENT the moment this profile is
	// next Saved for any reason (Save always writes currentVersion=2) —
	// including the user's own first enable/disable — at which point their
	// explicit choice sticks and this default stops recomputing. Fresh
	// profiles created after P1 start with none enabled by deliberate design
	// (empty skeleton first, docs/PLATFORM.md).
	const legacyDefaultPlugin = plugin.CloudAccountsID
	if p.Version < 2 {
		p.EnabledPlugins = ensureContains(p.EnabledPlugins, legacyDefaultPlugin)
	}
	return p, nil
}

// ensureContains returns ids with id appended if not already present.
func ensureContains(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

// writeProfile marshals p and replaces its profile.json atomically (temp
// file in the same directory, chmod 0600, rename) — same discipline as
// internal/workspace's write.
func writeProfile(root string, p Profile) error {
	dir := filepath.Join(root, p.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".profile.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(dir, "profile.json"))
}

// generateID returns a 32-hex-char crypto-random identifier. It doubles as
// the profile's folder name, so List/Get can cheaply verify a folder wasn't
// tampered with or copied incorrectly (see readProfile's ID/dirName check).
func generateID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
