package profile

import (
	"os"
	"path/filepath"
	"testing"

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
