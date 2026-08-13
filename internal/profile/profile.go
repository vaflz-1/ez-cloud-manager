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
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
	// v3: P1.5 core/plugin decoupling (docs/PLATFORM.md principle 5) —
	// Accounts/ShowAllAccounts move off the core Profile into
	// Settings[plugin.CloudAccountsID] (see settings.go's
	// CloudAccountsSettings); see readProfile's second legacy migration block.
	// v4: SavedAt records explicit core-profile saves independently from
	// UpdatedAt, which also changes for addon enablement and settings writes.
	currentVersion = 4
	// maxNameLen bounds a profile name, measured in Unicode code points.
	maxNameLen = 64
	// maxEnvVars caps env vars per profile — generous for real use, small
	// enough that a profile.json can never balloon.
	maxEnvVars = 200
	// maxEnabledPlugins caps enabled-plugin ids per profile — same rationale
	// as maxEnvVars.
	maxEnabledPlugins = 50
	// maxSettingsBlobs caps the number of per-plugin settings blobs a profile
	// may carry — same rationale as maxEnvVars/maxEnabledPlugins.
	maxSettingsBlobs = 50
	// maxSettingsBlobBytes caps any single plugin's settings blob size.
	maxSettingsBlobBytes = 16 << 10 // 16 KiB
	// maxProfileIDBytes keeps externally supplied IDs to one small path
	// component. Generated IDs are 32 bytes, while the wider cap preserves
	// read compatibility with early hand-authored/legacy IDs.
	maxProfileIDBytes = 128
	// maxProfileFileBytes bounds a tampered profile.json before JSON decoding.
	// It matches the per-entry ceiling used by .ezprofile import.
	maxProfileFileBytes = 4 << 20 // 4 MiB
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

// Profile is a global profile: the unit one app window binds to. Per
// docs/PLATFORM.md principle 5 ("core owns no plugin data"), core holds only
// name/env vars/enabled plugins/settings/window state — anything
// domain-specific (e.g. the Cloud Accounts scoping list) lives in Settings
// under the owning plugin's id, never as a top-level field here.
type Profile struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	EnvVars []EnvVar `json:"envVars"`
	// EnabledPlugins is a P1 placeholder — always empty until the plugin
	// host (PLATFORM.md phase P1) exists.
	EnabledPlugins []string `json:"enabledPlugins"`
	// Settings is the general per-plugin settings mechanism (P1.5): one
	// opaque JSON blob per plugin id, e.g. Settings[plugin.CloudAccountsID]
	// holds that plugin's account-scoping choice (see settings.go's
	// CloudAccountsSettings). internal/profile validates only the
	// cloud-accounts key's shape (normalizeCloudAccountsBlob in
	// validate.go); every other plugin's blob is opaque, unvalidated JSON.
	Settings map[string]json.RawMessage `json:"settings,omitempty"`
	// WindowState is opaque to Go — the Swift UI owns its shape (P0 does not
	// populate or read it yet; it exists so P1 needs no data migration).
	WindowState json.RawMessage `json:"windowState,omitempty"`
	Version     int             `json:"version"`
	CreatedAt   string          `json:"createdAt"`
	SavedAt     string          `json:"savedAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

// CoreUpdate is the subset of a Profile owned by the profile editor. Plugin
// enablement, plugin settings and window state have independent writers and
// must never be replaced from a potentially stale whole-profile UI draft.
// ExpectedName/ExpectedEnvVars are the editor's baseline and form a
// compare-and-swap guard against another process changing the same core data.
type CoreUpdate struct {
	ID                string
	Name              string
	EnvVars           []EnvVar
	ExpectedName      string
	ExpectedEnvVars   []EnvVar
	ExpectedUpdatedAt string
}

// ErrCoreConflict means a profile editor tried to save a draft based on core
// fields that another process has since changed. Addon-only changes never
// trigger it because their state is outside the compared core snapshot.
var ErrCoreConflict = errors.New("profile core changed since draft was loaded")

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
	var err error
	id, err = validateProfileID(id)
	if err != nil {
		return Profile{}, err
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
func Create(root string, p Profile) (created Profile, err error) {
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return createWithRootLockHeld(root, p)
}

// createWithRootLockHeld is Create's implementation for callers that already
// own the root lock (Duplicate and Import). Keeping the check and write in one
// critical section prevents two processes from validating the same free name
// and then both committing it. It must never acquire the root lock itself.
func createWithRootLockHeld(root string, p Profile) (Profile, error) {
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
	now := nextProfileTimestamp()
	normalized.ID = id
	normalized.Version = currentVersion
	normalized.CreatedAt = now
	normalized.SavedAt = now
	normalized.UpdatedAt = now
	if err := writeProfile(root, normalized); err != nil {
		return Profile{}, err
	}
	return normalized, nil
}

// Save re-validates and rewrites an existing profile in place, identified by
// p.ID (which must already exist). The name may change but still must not
// collide with a DIFFERENT profile. CreatedAt is preserved; UpdatedAt is
// refreshed. Save remains the whole-object replacement API, but participates
// in the same per-profile lock as targeted mutations so it cannot interleave
// a read/write cycle with them.
func Save(root string, p Profile) error {
	_, err := mutateProfileWithRootLock(root, p.ID, func(current *Profile) error {
		previousSavedAt := current.SavedAt
		previousUpdatedAt := current.UpdatedAt
		*current = p
		current.SavedAt = nextProfileTimestamp(previousSavedAt, previousUpdatedAt)
		return nil
	})
	return err
}

// UpdateCore replaces only the fields owned by the profile editor. It loads
// the latest profile before applying the update, so EnabledPlugins, Settings
// and WindowState are preserved even when the caller's draft predates changes
// made by a plugin-specific surface. The returned profile includes the saved
// timestamp assigned by Save.
func UpdateCore(root string, update CoreUpdate) (Profile, error) {
	hasCoreBaseline := strings.TrimSpace(update.ExpectedName) != "" ||
		update.ExpectedEnvVars != nil
	if hasCoreBaseline &&
		(strings.TrimSpace(update.ExpectedName) == "" || update.ExpectedEnvVars == nil) {
		return Profile{}, errors.New("expected profile name and envVars must be provided together")
	}
	if !hasCoreBaseline && strings.TrimSpace(update.ExpectedUpdatedAt) == "" {
		return Profile{}, errors.New("expected profile core snapshot or updatedAt is required")
	}
	return mutateProfileWithRootLock(root, update.ID, func(current *Profile) error {
		coreConflict := hasCoreBaseline &&
			(current.Name != update.ExpectedName ||
				!slices.Equal(current.EnvVars, update.ExpectedEnvVars))
		legacyConflict := !hasCoreBaseline &&
			current.UpdatedAt != update.ExpectedUpdatedAt
		if coreConflict || legacyConflict {
			return fmt.Errorf(
				"%w: profile %q was edited by another process; reload and review the preserved draft before saving",
				ErrCoreConflict,
				update.ID,
			)
		}
		current.Name = update.Name
		current.EnvVars = update.EnvVars
		current.SavedAt = nextProfileTimestamp(current.SavedAt, current.UpdatedAt)
		return nil
	})
}

// UpdateEnabledPlugins applies a batch of enable/disable decisions to the
// latest profile and replaces no other field. This package deliberately does
// not require ids to exist in the built-in registry: callers that expose a
// fixed catalog validate that policy before calling, while future/third-party
// ids already stored in the profile continue to round-trip untouched.
func UpdateEnabledPlugins(root, id string, changes map[string]bool) (Profile, error) {
	changedIDs := make([]string, 0, len(changes))
	for pluginID := range changes {
		changedIDs = append(changedIDs, pluginID)
	}
	sort.Strings(changedIDs)
	return mutateProfile(root, id, func(current *Profile) error {
		for _, pluginID := range changedIDs {
			current.EnabledPlugins = setPluginEnabled(current.EnabledPlugins, pluginID, changes[pluginID])
		}
		return nil
	})
}

// mutateProfileWithRootLock is the entry point for existing-profile writes
// that may change Name. It establishes the only permitted nested lock order:
// root first, then mutateProfile's per-profile lock. Targeted writers whose
// patches cannot alter Name continue to use mutateProfile directly, retaining
// independent per-profile concurrency.
func mutateProfileWithRootLock(root, id string, patch func(*Profile) error) (saved Profile, err error) {
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return mutateProfile(root, id, patch)
}

// mutateProfile is the single read-patch-validate-write path for an existing
// profile. The stable advisory lock spans every step, including name-collision
// validation and writeProfile's atomic rename, so cooperating processes cannot
// base targeted updates on the same stale snapshot and lose one another's
// fields.
func mutateProfile(root, id string, patch func(*Profile) error) (saved Profile, err error) {
	id, err = validateProfileID(id)
	if err != nil {
		return Profile{}, err
	}
	release, err := acquireProfileLock(root, id)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	current, err := Get(root, id)
	if err != nil {
		return Profile{}, err
	}
	createdAt := current.CreatedAt
	previousUpdatedAt := current.UpdatedAt
	if err := patch(&current); err != nil {
		return Profile{}, err
	}
	normalized, err := validateProfile(current)
	if err != nil {
		return Profile{}, err
	}
	list, err := List(root)
	if err != nil {
		return Profile{}, err
	}
	if nameTaken(list, normalized.Name, id) {
		return Profile{}, fmt.Errorf("profile %q already exists", normalized.Name)
	}

	normalized.ID = id
	normalized.Version = currentVersion
	normalized.CreatedAt = createdAt
	normalized.SavedAt = current.SavedAt
	if normalized.SavedAt == "" {
		normalized.SavedAt = createdAt
	}
	// UpdatedAt is both a user-visible modification time and the CAS token
	// accepted from legacy whole-profile clients. It therefore must change on
	// every committed mutation even when two writes share a wall-clock tick or
	// the system clock moves backwards. Including SavedAt also preserves the
	// natural ordering UpdatedAt >= SavedAt for explicit core saves.
	normalized.UpdatedAt = nextProfileTimestamp(previousUpdatedAt, normalized.SavedAt)
	if err := writeProfile(root, normalized); err != nil {
		return Profile{}, err
	}
	// Return the canonical on-disk representation (including RawMessage
	// indentation), matching the targeted writers' pre-lock API behavior.
	return Get(root, id)
}

// setPluginEnabled returns ids with pluginID added (enabled) or removed
// (!enabled), never duplicated.
func setPluginEnabled(ids []string, pluginID string, enabled bool) []string {
	out := make([]string, 0, len(ids)+1)
	found := false
	for _, id := range ids {
		if id == pluginID {
			found = true
			if !enabled {
				continue
			}
		}
		out = append(out, id)
	}
	if enabled && !found {
		out = append(out, pluginID)
	}
	return out
}

// Rename changes a profile's name, rejecting a collision with a different
// profile. Renaming to the current name is allowed (a no-op rewrite).
func Rename(root, id, newName string) error {
	newName = strings.TrimSpace(newName)
	if err := validateName(newName); err != nil {
		return err
	}
	_, err := mutateProfileWithRootLock(root, id, func(p *Profile) error {
		p.Name = newName
		p.SavedAt = nextProfileTimestamp(p.SavedAt, p.UpdatedAt)
		return nil
	})
	return err
}

// Duplicate copies an existing profile's EnvVars/EnabledPlugins/Settings
// (including whichever plugin settings blobs it carries, e.g. Cloud
// Accounts' scoping) under a fresh ID. An empty newName defaults to
// "<original> copy", deduped against existing names; an explicit newName
// that collides is rejected.
func Duplicate(root, id, newName string) (created Profile, err error) {
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

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

	return createWithRootLockHeld(root, Profile{
		Name:           name,
		EnvVars:        src.EnvVars,
		EnabledPlugins: src.EnabledPlugins,
		Settings:       cloneSettings(src.Settings),
	})
}

// Delete removes a profile folder while holding the root lock followed by the
// same per-profile lock as mutations. The root lock makes the "at least one"
// check and removal indivisible across different profile IDs; the per-profile
// lock prevents a writer that read before deletion from recreating the folder.
func Delete(root, id string) (err error) {
	id, err = validateProfileID(id)
	if err != nil {
		return err
	}
	releaseRoot, err := acquireRootLock(root)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := releaseRoot(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	release, err := acquireProfileLock(root, id)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

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
	var err error
	dirName, err = validateProfileID(dirName)
	if err != nil {
		return Profile{}, err
	}
	path := filepath.Join(root, dirName, "profile.json")
	data, err := readProfileFile(path)
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
	if p.EnvVars == nil {
		p.EnvVars = []EnvVar{}
	}
	if p.EnabledPlugins == nil {
		p.EnabledPlugins = []string{}
	}
	if p.SavedAt == "" {
		p.SavedAt = p.UpdatedAt
		if p.SavedAt == "" {
			p.SavedAt = p.CreatedAt
		}
	}
	// legacyDefaultPlugin is auto-enabled (in memory, on every read) for a
	// profile written by a pre-P1 build (Version < 2) — so an existing
	// user's window keeps showing what it always showed (the credentials
	// browser) instead of a suddenly-empty hub. This is intentionally NOT a
	// batch migration pass: it becomes PERMANENT the moment this profile is
	// next Saved for any reason (Save always writes currentVersion) —
	// including the user's own first enable/disable — at which point their
	// explicit choice sticks and this default stops recomputing. Fresh
	// profiles created after P1 start with none enabled by deliberate design
	// (empty skeleton first, docs/PLATFORM.md).
	const legacyDefaultPlugin = plugin.CloudAccountsID
	if p.Version < 2 {
		p.EnabledPlugins = ensureContains(p.EnabledPlugins, legacyDefaultPlugin)
	}
	// P1.5 (Version < 3): the on-disk JSON may still carry the old top-level
	// "accounts"/"showAllAccounts" keys, which Profile no longer declares (so
	// the json.Unmarshal above silently ignored them). Re-parse the SAME
	// bytes into a throwaway struct that still has those fields, and — if
	// either was actually set — fold them into
	// Settings[plugin.CloudAccountsID] so nothing is lost. Same "self-erasing
	// migration" idiom as the Version<2 block above: in-memory only, becomes
	// permanent (and the stray top-level keys stop being written) the moment
	// this profile is next Saved (Save always writes currentVersion). Skipped
	// entirely if a cloud-accounts blob already exists (an explicit P1.5+
	// choice always wins over a stale legacy value).
	if p.Version < 3 {
		var legacy struct {
			ShowAllAccounts bool         `json:"showAllAccounts"`
			Accounts        []AccountRef `json:"accounts"`
		}
		_ = json.Unmarshal(data, &legacy)
		if len(legacy.Accounts) > 0 || legacy.ShowAllAccounts {
			if p.Settings == nil {
				p.Settings = map[string]json.RawMessage{}
			}
			if _, exists := p.Settings[plugin.CloudAccountsID]; !exists {
				blob, err := json.Marshal(CloudAccountsSettings{
					ShowAllAccounts: legacy.ShowAllAccounts,
					Accounts:        legacy.Accounts,
				})
				if err == nil {
					p.Settings[plugin.CloudAccountsID] = blob
				}
			}
		}
	}

	// A profile can be modified outside this process (sync tools, restore,
	// hand-editing). Re-apply the same validation used by Create/Save after all
	// legacy migrations, otherwise a tampered file could bypass the secret and
	// subprocess-hijack environment guards merely by already being on disk.
	normalized, err := validateProfile(p)
	if err != nil {
		return Profile{}, fmt.Errorf("validate %s: %w", path, err)
	}
	p.Name = normalized.Name
	p.EnvVars = normalized.EnvVars
	p.EnabledPlugins = normalized.EnabledPlugins
	p.Settings = normalized.Settings
	p.WindowState = normalized.WindowState
	return p, nil
}

func readProfileFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxProfileFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProfileFileBytes {
		return nil, fmt.Errorf("profile file %s exceeds the %d MiB size limit", path, maxProfileFileBytes>>20)
	}
	return data, nil
}

// validateProfileID requires exactly one local path component. Generated IDs
// are lowercase hex, but older test/dev builds admitted other component-safe
// IDs, so validation preserves those while rejecting traversal, separators,
// volume names and control characters before any filesystem access.
func validateProfileID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("profile id is required")
	}
	if len(id) > maxProfileIDBytes {
		return "", fmt.Errorf("profile id must be at most %d bytes", maxProfileIDBytes)
	}
	if id == "." || id == ".." || !filepath.IsLocal(id) || filepath.Base(id) != id ||
		filepath.VolumeName(id) != "" || strings.ContainsAny(id, `/\\`) || hasControlChars(id) {
		return "", fmt.Errorf("profile id %q must be one safe path component", id)
	}
	return id, nil
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
	id, err := validateProfileID(p.ID)
	if err != nil {
		return err
	}
	p.ID = id

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxProfileFileBytes {
		return fmt.Errorf("profile %q exceeds the %d MiB size limit", p.ID, maxProfileFileBytes>>20)
	}

	dir := filepath.Join(root, p.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

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

// nextProfileTimestamp returns an RFC3339Nano timestamp strictly later than
// every valid timestamp in previous. time.Now normally supplies that value;
// when writes share a clock tick or the wall clock moves backwards, advancing
// the greatest previous value by one nanosecond keeps SavedAt/UpdatedAt
// monotonic and makes UpdatedAt safe as the legacy compare-and-swap token.
func nextProfileTimestamp(previous ...string) string {
	next := time.Now().UTC()
	for _, raw := range previous {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if !next.After(parsed) {
			next = parsed.Add(time.Nanosecond)
		}
	}
	return next.Format(time.RFC3339Nano)
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
