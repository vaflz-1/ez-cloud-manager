package profile

import (
	"os"
	"path/filepath"
	"testing"

	"ez-cloud-manager/internal/plugin"
	"ez-cloud-manager/internal/workspace"
)

// seedLegacyWorkspaces points EZCLOUD_CONFIG_DIR at a temp dir and writes the
// given workspaces there via the (untouched) v1.1 package.
func seedLegacyWorkspaces(t *testing.T, workspaces ...workspace.Workspace) {
	t.Helper()
	t.Setenv("EZCLOUD_CONFIG_DIR", t.TempDir())
	path, err := workspace.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range workspaces {
		if err := workspace.Save(path, w); err != nil {
			t.Fatalf("seed workspace %q: %v", w.Name, err)
		}
	}
}

func TestMigrateCreatesOneProfilePerWorkspace(t *testing.T) {
	seedLegacyWorkspaces(t,
		workspace.Workspace{Name: "acme-prod", Members: []workspace.Member{{Provider: "aws", Profile: "prod"}}},
		workspace.Workspace{Name: "acme-staging", Members: []workspace.Member{{Provider: "aws", Profile: "staging"}}},
	)

	root := tmpRoot(t)
	migrated, err := MigrateFromWorkspaces(root)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if migrated != 2 {
		t.Fatalf("migrated = %d, want 2", migrated)
	}

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 profiles, got %d", len(list))
	}
	byName := map[string]Profile{}
	for _, p := range list {
		byName[p.Name] = p
	}
	prod, ok := byName["acme-prod"]
	if !ok {
		t.Fatalf("expected an acme-prod profile, got %+v", list)
	}
	if len(prod.Accounts) != 1 || prod.Accounts[0] != (AccountRef{Provider: "aws", Account: "prod"}) {
		t.Fatalf("accounts = %+v", prod.Accounts)
	}
}

func TestMigrateIdempotentViaMarker(t *testing.T) {
	seedLegacyWorkspaces(t, workspace.Workspace{Name: "acme", Members: []workspace.Member{{Provider: "aws", Profile: "p"}}})

	root := tmpRoot(t)
	if _, err := MigrateFromWorkspaces(root); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, migratedMarker)); err != nil {
		t.Fatalf("expected marker file: %v", err)
	}

	// A second legacy workspace shows up after the first migration (e.g. the
	// user briefly used an older build); the marker must stop it from being
	// picked up automatically.
	seedLegacyWorkspaces(t, workspace.Workspace{Name: "acme", Members: []workspace.Member{{Provider: "aws", Profile: "p"}}},
		workspace.Workspace{Name: "new-one", Members: []workspace.Member{{Provider: "aws", Profile: "p"}}})

	migrated, err := MigrateFromWorkspaces(root)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("second migrate should be a no-op due to the marker, migrated = %d", migrated)
	}
	list, _ := List(root)
	if len(list) != 1 {
		t.Fatalf("want 1 profile after marker short-circuit, got %d", len(list))
	}
}

func TestMigrateFreshInstallCreatesDefaultProfile(t *testing.T) {
	seedLegacyWorkspaces(t) // no legacy workspaces at all

	root := tmpRoot(t)
	migrated, err := MigrateFromWorkspaces(root)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1 (the Default profile)", migrated)
	}

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Default" {
		t.Fatalf("expected a single Default profile, got %+v", list)
	}
	if !list[0].ShowAllAccounts {
		t.Fatal("Default profile should show all accounts")
	}
}

func TestMigrateSkipsWorkspaceNameAlreadyPresent(t *testing.T) {
	seedLegacyWorkspaces(t, workspace.Workspace{Name: "prod", Members: []workspace.Member{{Provider: "aws", Profile: "p"}}})

	root := tmpRoot(t)
	// A profile with that name already exists (e.g. the user made one by
	// hand before migrating).
	mustCreate(t, root, Profile{Name: "prod"})

	migrated, err := MigrateFromWorkspaces(root)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("migrated = %d, want 0 (name already present)", migrated)
	}
	list, _ := List(root)
	if len(list) != 1 {
		t.Fatalf("want 1 profile (no duplicate), got %d", len(list))
	}
}

// TestMigrateFromWorkspacesPreEnablesCloudAccounts covers a P1 edge case in
// the v1.1 -> v2.0 migration path: a profile created HERE (from a real
// legacy workspace, with real account references) is written via Create,
// which always stamps currentVersion (2) — so it never takes the Version<2
// branch in readProfile that auto-enables cloud-accounts for a pre-P1
// profile already on disk (see profile.go). Without an explicit pre-enable
// in the per-workspace loop itself, a user whose very first ezcloud launch
// is already a P1+ build (no intermediate P0-only run in between) would get
// an empty Hub despite having real, already-configured cloud accounts — so
// MigrateFromWorkspaces sets EnabledPlugins directly for profiles it creates
// from a real legacy workspace. The "zero profiles" fallback (a truly fresh
// install, no legacy workspace at all) is deliberately excluded — that one
// still gets the empty-skeleton Default profile P1 intends.
func TestMigrateFromWorkspacesPreEnablesCloudAccounts(t *testing.T) {
	seedLegacyWorkspaces(t, workspace.Workspace{Name: "acme-prod", Members: []workspace.Member{{Provider: "aws", Profile: "prod"}}})

	root := tmpRoot(t)
	if _, err := MigrateFromWorkspaces(root); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 profile, got %d", len(list))
	}
	if len(list[0].EnabledPlugins) != 1 || list[0].EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("EnabledPlugins = %+v, want [%s] pre-enabled for a profile migrated from a real legacy workspace", list[0].EnabledPlugins, plugin.CloudAccountsID)
	}
}

// TestMigrateFreshInstallDoesNotPreEnableAnything ensures the fix above is
// scoped correctly: the "zero legacy workspaces" fallback Default profile
// must stay a genuinely empty skeleton, not accidentally pick up
// cloud-accounts too.
func TestMigrateFreshInstallDoesNotPreEnableAnything(t *testing.T) {
	seedLegacyWorkspaces(t) // no legacy workspaces at all

	root := tmpRoot(t)
	if _, err := MigrateFromWorkspaces(root); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Default" {
		t.Fatalf("expected a single Default profile, got %+v", list)
	}
	if len(list[0].EnabledPlugins) != 0 {
		t.Fatalf("EnabledPlugins = %+v, want empty for the fresh-install fallback Default profile", list[0].EnabledPlugins)
	}
}
