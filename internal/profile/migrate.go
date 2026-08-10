package profile

import (
	"encoding/json"
	"os"
	"path/filepath"

	"ez-cloud-manager/internal/plugin"
	"ez-cloud-manager/internal/workspace"
)

// migratedMarker's presence means MigrateFromWorkspaces already ran for this
// root. Migration is idempotent by name even without it (see below), but the
// marker makes the common case a fast no-op, so callers (e.g. the UI, on
// every app launch) can call this freely.
const migratedMarker = ".migrated"

// MigrateFromWorkspaces is the v1.1 → v2.0 upgrade path (PLATFORM.md: "the
// v1.1 internal/workspace package is the seed of the profile engine"). It
// reads the legacy workspaces.json — internal/workspace itself is untouched,
// used here only as a read-only migration source — and creates one Profile
// per legacy Workspace not already present by name.
//
// If the result would be zero profiles (fresh install, or an empty/absent
// legacy file), it creates a single "Default" profile with the cloud-accounts
// plugin's ShowAllAccounts settings blob set, so the app is never left
// without one: every window must bind to exactly one profile.
func MigrateFromWorkspaces(root string) (int, error) {
	markerPath := filepath.Join(root, migratedMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return 0, nil
	}

	existing, err := List(root)
	if err != nil {
		return 0, err
	}

	wsPath, err := workspace.DefaultPath()
	if err != nil {
		return 0, err
	}
	legacy, err := workspace.List(wsPath)
	if err != nil {
		return 0, err
	}

	migrated := 0
	for _, w := range legacy {
		if nameTaken(existing, w.Name, "") {
			continue
		}
		accounts := make([]AccountRef, 0, len(w.Members))
		for _, m := range w.Members {
			accounts = append(accounts, AccountRef{Provider: m.Provider, Account: m.Profile})
		}
		// A profile born from a real legacy workspace already has a working
		// credentials setup — pre-enable cloud-accounts here too, the same as
		// readProfile does in memory for a pre-P1 profile.json already on
		// disk (see profile.go). Without this, a user whose very first
		// ezcloud launch is already P1+ (no separate P0-only run in between)
		// would get an empty Hub despite having real, already-configured
		// accounts. The "zero profiles" fallback below stays untouched — a
		// truly fresh install (no legacy workspace at all) still gets the
		// empty-skeleton Default profile P1 intends. Accounts land in the
		// cloud-accounts plugin's own settings blob (P1.5), not a top-level
		// Profile field.
		blob, err := json.Marshal(CloudAccountsSettings{Accounts: accounts})
		if err != nil {
			return migrated, err
		}
		created, err := Create(root, Profile{
			Name:           w.Name,
			EnabledPlugins: []string{plugin.CloudAccountsID},
			Settings:       map[string]json.RawMessage{plugin.CloudAccountsID: blob},
		})
		if err != nil {
			return migrated, err
		}
		existing = append(existing, created)
		migrated++
	}

	if len(existing) == 0 {
		blob, err := json.Marshal(CloudAccountsSettings{ShowAllAccounts: true})
		if err != nil {
			return migrated, err
		}
		if _, err := Create(root, Profile{
			Name:     "Default",
			Settings: map[string]json.RawMessage{plugin.CloudAccountsID: blob},
		}); err != nil {
			return migrated, err
		}
		migrated++
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return migrated, err
	}
	if err := os.WriteFile(markerPath, []byte{}, 0o600); err != nil {
		return migrated, err
	}
	return migrated, nil
}
