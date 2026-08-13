package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ez-cloud-manager/internal/plugin"
)

func tmpRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "profiles")
}

func mustCreate(t *testing.T, root string, p Profile) Profile {
	t.Helper()
	created, err := Create(root, p)
	if err != nil {
		t.Fatalf("create %q: %v", p.Name, err)
	}
	return created
}

// cloudAccountsSettingsJSON builds a Profile.Settings map holding s under
// plugin.CloudAccountsID — the P1.5 replacement for setting
// Profile.Accounts/ShowAllAccounts directly (those fields moved into the
// Cloud Accounts plugin's own settings blob; see settings.go).
func cloudAccountsSettingsJSON(t *testing.T, s CloudAccountsSettings) map[string]json.RawMessage {
	t.Helper()
	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]json.RawMessage{plugin.CloudAccountsID: blob}
}

func TestCreateListRoundtrip(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{
		Name:     "prod",
		Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{{Provider: "aws", Account: "default"}}}),
	})
	if created.ID == "" {
		t.Fatal("expected a generated ID")
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 profile, got %d", len(list))
	}
	if list[0].Name != "prod" {
		t.Fatalf("name = %q", list[0].Name)
	}
	accounts := GetCloudAccountsSettings(list[0]).Accounts
	if len(accounts) != 1 || accounts[0] != (AccountRef{Provider: "aws", Account: "default"}) {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestCreateRejectsDuplicateNameCaseInsensitive(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "Prod"})
	if _, err := Create(root, Profile{Name: "prod"}); err == nil {
		t.Fatal("expected error creating a case-insensitive duplicate name")
	}
}

func TestIDStableAcrossSave(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a"})
	created.Name = "a-renamed"
	if err := Save(root, created); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Fatalf("id changed across save: %q -> %q", created.ID, got.ID)
	}
	if got.Name != "a-renamed" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.CreatedAt != created.CreatedAt {
		t.Fatalf("createdAt should be preserved: %q -> %q", created.CreatedAt, got.CreatedAt)
	}
}

func TestUpdateCorePreservesPluginOwnedState(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{
		Name:    "before",
		EnvVars: []EnvVar{{Key: "REGION", Value: "us-east-1"}},
		EnabledPlugins: []string{
			"some-future-plugin",
			plugin.CloudAccountsID,
		},
		Settings: map[string]json.RawMessage{
			plugin.CloudAccountsID: json.RawMessage(`{"showAllAccounts":false,"accounts":[{"provider":"aws","account":"prod"}]}`),
			"some-future-plugin":   json.RawMessage(`{"theme":"night"}`),
		},
		WindowState: json.RawMessage(`{"selectedTab":"plugins","width":900}`),
	})

	saved, err := UpdateCore(root, CoreUpdate{
		ID:              created.ID,
		Name:            "after",
		EnvVars:         []EnvVar{{Key: "REGION", Value: "eu-west-1"}},
		ExpectedName:    created.Name,
		ExpectedEnvVars: created.EnvVars,
	})
	if err != nil {
		t.Fatalf("UpdateCore: %v", err)
	}
	if saved.ID != created.ID || saved.Name != "after" {
		t.Fatalf("saved identity/core fields = %+v", saved)
	}
	if len(saved.EnvVars) != 1 || saved.EnvVars[0] != (EnvVar{Key: "REGION", Value: "eu-west-1"}) {
		t.Fatalf("env vars = %+v", saved.EnvVars)
	}
	if saved.CreatedAt != created.CreatedAt || saved.SavedAt == "" || saved.UpdatedAt == "" {
		t.Fatalf(
			"timestamps = created %q -> %q, saved %q, updated %q",
			created.CreatedAt,
			saved.CreatedAt,
			saved.SavedAt,
			saved.UpdatedAt,
		)
	}
	if len(saved.EnabledPlugins) != 2 || saved.EnabledPlugins[0] != "some-future-plugin" || saved.EnabledPlugins[1] != plugin.CloudAccountsID {
		t.Fatalf("enabled plugins were replaced: %+v", saved.EnabledPlugins)
	}
	accounts := GetCloudAccountsSettings(saved).Accounts
	if len(accounts) != 1 || accounts[0] != (AccountRef{Provider: "aws", Account: "prod"}) {
		t.Fatalf("cloud-accounts settings were replaced: %+v", accounts)
	}
	var opaque struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(saved.Settings["some-future-plugin"], &opaque); err != nil || opaque.Theme != "night" {
		t.Fatalf("opaque settings were replaced: %s (%v)", saved.Settings["some-future-plugin"], err)
	}
	var windowState struct {
		SelectedTab string `json:"selectedTab"`
		Width       int    `json:"width"`
	}
	if err := json.Unmarshal(saved.WindowState, &windowState); err != nil {
		t.Fatalf("decode window state: %v", err)
	}
	if windowState.SelectedTab != "plugins" || windowState.Width != 900 {
		t.Fatalf("window state was replaced: %+v", windowState)
	}
}

func TestUpdateCoreRejectsStaleDraftWithoutConflictingWithAddonWrites(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{
		Name:    "before",
		EnvVars: []EnvVar{{Key: "REGION", Value: "us-east-1"}},
	})
	if created.SavedAt == "" {
		t.Fatal("Create must set savedAt")
	}

	addonSaved, err := UpdateEnabledPlugins(
		root,
		created.ID,
		map[string]bool{plugin.TransferID: true},
	)
	if err != nil {
		t.Fatalf("UpdateEnabledPlugins: %v", err)
	}
	if addonSaved.SavedAt != created.SavedAt {
		t.Fatalf("addon write changed savedAt: %q -> %q", created.SavedAt, addonSaved.SavedAt)
	}

	firstCoreSave, err := UpdateCore(root, CoreUpdate{
		ID:              created.ID,
		Name:            "first editor",
		EnvVars:         []EnvVar{{Key: "REGION", Value: "eu-west-1"}},
		ExpectedName:    created.Name,
		ExpectedEnvVars: created.EnvVars,
	})
	if err != nil {
		t.Fatalf("UpdateCore after addon-only change: %v", err)
	}
	if len(firstCoreSave.EnabledPlugins) != 1 ||
		firstCoreSave.EnabledPlugins[0] != plugin.TransferID {
		t.Fatalf("addon state was lost: %+v", firstCoreSave.EnabledPlugins)
	}

	_, err = UpdateCore(root, CoreUpdate{
		ID:              created.ID,
		Name:            "stale editor",
		EnvVars:         []EnvVar{{Key: "REGION", Value: "ap-southeast-1"}},
		ExpectedName:    created.Name,
		ExpectedEnvVars: created.EnvVars,
	})
	if !errors.Is(err, ErrCoreConflict) {
		t.Fatalf("stale UpdateCore error = %v, want ErrCoreConflict", err)
	}

	persisted, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Name != firstCoreSave.Name ||
		!slices.Equal(persisted.EnvVars, firstCoreSave.EnvVars) {
		t.Fatalf("stale draft changed persisted core: %+v", persisted)
	}
}

// TestUpdateCoreLegacyCASRejectsSameExpectedTimestamp deterministically
// reproduces the old second-resolution failure: two legacy saves start from
// one ExpectedUpdatedAt while the wall clock is behind that value. The first
// save must advance UpdatedAt past the previous timestamp; the second must see
// a different CAS token and preserve the first save.
func TestUpdateCoreLegacyCASRejectsSameExpectedTimestamp(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{
		Name:    "baseline",
		EnvVars: []EnvVar{{Key: "REGION", Value: "us-east-1"}},
	})

	// A future timestamp makes the clock-backward case deterministic and also
	// forces both mutations into the same RFC3339 second. The timestamp helper
	// must advance this value by nanoseconds rather than reuse it.
	const sharedExpected = "2999-01-01T00:00:00Z"
	created.SavedAt = sharedExpected
	created.UpdatedAt = sharedExpected
	writeRawProfile(t, root, created)

	first, err := UpdateCore(root, CoreUpdate{
		ID:                created.ID,
		Name:              "first legacy editor",
		EnvVars:           []EnvVar{{Key: "REGION", Value: "eu-west-1"}},
		ExpectedUpdatedAt: sharedExpected,
	})
	if err != nil {
		t.Fatalf("first legacy UpdateCore: %v", err)
	}

	expectedTime, err := time.Parse(time.RFC3339Nano, sharedExpected)
	if err != nil {
		t.Fatal(err)
	}
	savedTime, err := time.Parse(time.RFC3339Nano, first.SavedAt)
	if err != nil {
		t.Fatalf("savedAt is not RFC3339Nano: %q: %v", first.SavedAt, err)
	}
	updatedTime, err := time.Parse(time.RFC3339Nano, first.UpdatedAt)
	if err != nil {
		t.Fatalf("updatedAt is not RFC3339Nano: %q: %v", first.UpdatedAt, err)
	}
	if !savedTime.After(expectedTime) {
		t.Fatalf("savedAt did not advance past backward clock floor: %q <= %q", first.SavedAt, sharedExpected)
	}
	if !updatedTime.After(savedTime) {
		t.Fatalf("updatedAt = %q, want strictly after savedAt %q", first.UpdatedAt, first.SavedAt)
	}

	_, err = UpdateCore(root, CoreUpdate{
		ID:                created.ID,
		Name:              "stale legacy editor",
		EnvVars:           []EnvVar{{Key: "REGION", Value: "ap-southeast-1"}},
		ExpectedUpdatedAt: sharedExpected,
	})
	if !errors.Is(err, ErrCoreConflict) {
		t.Fatalf("second legacy UpdateCore error = %v, want ErrCoreConflict", err)
	}

	persisted, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Name != first.Name || !slices.Equal(persisted.EnvVars, first.EnvVars) {
		t.Fatalf("stale legacy save replaced first save: %+v", persisted)
	}
}

func TestSaveRejectsCollisionWithDifferentProfile(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "a"})
	b := mustCreate(t, root, Profile{Name: "b"})

	b.Name = "a"
	if err := Save(root, b); err == nil {
		t.Fatal("expected error saving onto another profile's name")
	}
}

func TestSaveMissingIDFails(t *testing.T) {
	root := tmpRoot(t)
	if err := Save(root, Profile{Name: "ghost"}); err == nil {
		t.Fatal("expected error saving without an id")
	}
}

func TestRename(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "old"})
	if err := Rename(root, created.ID, "new"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestRenameToExistingFails(t *testing.T) {
	root := tmpRoot(t)
	a := mustCreate(t, root, Profile{Name: "a"})
	mustCreate(t, root, Profile{Name: "b"})
	if err := Rename(root, a.ID, "b"); err == nil {
		t.Fatal("expected error renaming onto an existing name")
	}
}

func TestRenameToSameNameIsAllowed(t *testing.T) {
	root := tmpRoot(t)
	a := mustCreate(t, root, Profile{Name: "a"})
	if err := Rename(root, a.ID, "a"); err != nil {
		t.Fatalf("rename to same name should be a no-op, got %v", err)
	}
}

func TestDuplicate(t *testing.T) {
	root := tmpRoot(t)
	src := mustCreate(t, root, Profile{
		Name:     "prod",
		Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{{Provider: "aws", Account: "default"}}}),
		EnvVars:  []EnvVar{{Key: "REGION", Value: "us-east-1"}},
	})

	dup, err := Duplicate(root, src.ID, "")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if dup.ID == src.ID {
		t.Fatal("duplicate should get a new ID")
	}
	if dup.Name != "prod copy" {
		t.Fatalf("default duplicate name = %q", dup.Name)
	}
	srcAccounts := GetCloudAccountsSettings(src).Accounts
	dupAccounts := GetCloudAccountsSettings(dup).Accounts
	if len(dupAccounts) != 1 || dupAccounts[0] != srcAccounts[0] {
		t.Fatalf("accounts not copied: %+v", dupAccounts)
	}
	if len(dup.EnvVars) != 1 || dup.EnvVars[0] != src.EnvVars[0] {
		t.Fatalf("env vars not copied: %+v", dup.EnvVars)
	}
}

func TestDuplicateWithExplicitName(t *testing.T) {
	root := tmpRoot(t)
	src := mustCreate(t, root, Profile{Name: "prod"})
	dup, err := Duplicate(root, src.ID, "staging")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if dup.Name != "staging" {
		t.Fatalf("name = %q", dup.Name)
	}
}

func TestDuplicateExplicitNameCollisionFails(t *testing.T) {
	root := tmpRoot(t)
	src := mustCreate(t, root, Profile{Name: "prod"})
	mustCreate(t, root, Profile{Name: "staging"})
	if _, err := Duplicate(root, src.ID, "staging"); err == nil {
		t.Fatal("expected error duplicating onto an existing name")
	}
}

func TestDeleteLastProfileIsRefused(t *testing.T) {
	root := tmpRoot(t)
	only := mustCreate(t, root, Profile{Name: "only"})
	if err := Delete(root, only.ID); err == nil {
		t.Fatal("expected error deleting the last remaining profile")
	}
}

func TestDeleteRemovesProfile(t *testing.T) {
	root := tmpRoot(t)
	a := mustCreate(t, root, Profile{Name: "a"})
	mustCreate(t, root, Profile{Name: "b"})

	if err := Delete(root, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ := List(root)
	if len(list) != 1 || list[0].Name != "b" {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestDeleteMissingFails(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "a"})
	if err := Delete(root, "nonexistent"); err == nil {
		t.Fatal("expected error deleting an absent profile")
	}
}

func TestShowAllAccountsRoundtrips(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{ShowAllAccounts: true})})
	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !GetCloudAccountsSettings(got).ShowAllAccounts {
		t.Fatal("showAllAccounts should round-trip true")
	}
}

func TestListSkipsCorruptFolder(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "good"})

	badDir := filepath.Join(root, "not-json-id")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "profile.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list should not fail on a corrupt entry: %v", err)
	}
	if len(list) != 1 || list[0].Name != "good" {
		t.Fatalf("expected only the good profile, got %+v", list)
	}
}

func TestGetRejectsTraversalIDBeforeFilesystemAccess(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "profiles")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":"../outside","name":"escaped","version":4}`
	if err := os.WriteFile(filepath.Join(outside, "profile.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Get(root, "../outside"); err == nil {
		t.Fatal("Get accepted a traversal profile id")
	}
	if err := Delete(root, "../outside"); err == nil {
		t.Fatal("Delete accepted a traversal profile id")
	}
}

func TestGetRejectsTamperedPersistedEnvironment(t *testing.T) {
	for _, env := range []EnvVar{
		{Key: "DYLD_INSERT_LIBRARIES", Value: "/tmp/evil.dylib"},
		{Key: "AWS_SECRET_ACCESS_KEY", Value: "must-not-load"},
	} {
		t.Run(env.Key, func(t *testing.T) {
			root := tmpRoot(t)
			created := mustCreate(t, root, Profile{Name: "safe"})
			created.EnvVars = []EnvVar{env}
			writeRawProfile(t, root, created)

			if _, err := Get(root, created.ID); err == nil {
				t.Fatalf("Get accepted tampered environment key %q", env.Key)
			}
		})
	}
}

func TestGetRejectsOversizedProfileBeforeJSONDecode(t *testing.T) {
	root := tmpRoot(t)
	id := strings.Repeat("a", 32)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "profile.json"),
		[]byte(strings.Repeat(" ", maxProfileFileBytes+1)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := Get(root, id); err == nil {
		t.Fatal("Get accepted an oversized profile file")
	}
}

func TestListSkipsMismatchedID(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "good"})

	dir := filepath.Join(root, "folder-name")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mismatched := Profile{ID: "some-other-id", Name: "mismatched", Version: currentVersion}
	data, _ := json.Marshal(mismatched)
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "good" {
		t.Fatalf("expected mismatched-id folder to be skipped, got %+v", list)
	}
}

func TestListSkipsNewerVersion(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "good"})

	dir := filepath.Join(root, "future-id")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	future := Profile{ID: "future-id", Name: "future", Version: currentVersion + 1}
	data, _ := json.Marshal(future)
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "good" {
		t.Fatalf("expected newer-schema folder to be skipped, got %+v", list)
	}
}

func TestListMissingRoot(t *testing.T) {
	list, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestCreateWritesRestrictivePerms(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a"})
	info, err := os.Stat(filepath.Join(root, created.ID, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestValidationRejections(t *testing.T) {
	root := tmpRoot(t)
	longName := strings.Repeat("x", maxNameLen+1)
	cases := []struct {
		desc string
		p    Profile
	}{
		{"empty name", Profile{Name: "  "}},
		{"too long name", Profile{Name: longName}},
		{"control char name", Profile{Name: "bad\nname"}},
		{"empty account provider", Profile{Name: "ok1", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{{Provider: " ", Account: "p"}}})}},
		{"empty account name", Profile{Name: "ok2", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{{Provider: "aws", Account: ""}}})}},
		{"control char account", Profile{Name: "ok3", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{{Provider: "aws", Account: "p\x00"}}})}},
		{"empty env key", Profile{Name: "ok4", EnvVars: []EnvVar{{Key: " ", Value: "x"}}}},
		{"control char env key", Profile{Name: "ok5", EnvVars: []EnvVar{{Key: "bad\nkey", Value: "x"}}}},
		{"secret-looking env key", Profile{Name: "ok6", EnvVars: []EnvVar{{Key: "AWS_SECRET_ACCESS_KEY", Value: "x"}}}},
		{"token-looking env key", Profile{Name: "ok7", EnvVars: []EnvVar{{Key: "API_TOKEN", Value: "x"}}}},
		{"password-looking env key", Profile{Name: "ok8", EnvVars: []EnvVar{{Key: "DB_PASSWORD", Value: "x"}}}},
		{"hijack var DYLD_INSERT_LIBRARIES", Profile{Name: "ok9", EnvVars: []EnvVar{{Key: "DYLD_INSERT_LIBRARIES", Value: "/tmp/evil.dylib"}}}},
		{"hijack var LD_PRELOAD", Profile{Name: "ok10", EnvVars: []EnvVar{{Key: "LD_PRELOAD", Value: "/tmp/evil.so"}}}},
		{"hijack var NODE_OPTIONS", Profile{Name: "ok11", EnvVars: []EnvVar{{Key: "NODE_OPTIONS", Value: "--require /tmp/evil.js"}}}},
		{"hijack var PATH", Profile{Name: "ok12", EnvVars: []EnvVar{{Key: "PATH", Value: "/tmp/evil:/usr/bin"}}}},
		{"hijack var lowercase path", Profile{Name: "ok13", EnvVars: []EnvVar{{Key: "path", Value: "/tmp/evil"}}}},
		{"hijack var mixed-case Dyld_Insert_Libraries", Profile{Name: "ok14", EnvVars: []EnvVar{{Key: "Dyld_Insert_Libraries", Value: "/tmp/evil.dylib"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			if _, err := Create(root, tc.p); err == nil {
				t.Fatalf("expected validation error for %s", tc.desc)
			}
		})
	}
}

func TestValidationAcceptsNonSecretEnvVar(t *testing.T) {
	root := tmpRoot(t)
	if _, err := Create(root, Profile{Name: "ok", EnvVars: []EnvVar{{Key: "AWS_REGION", Value: "us-east-1"}}}); err != nil {
		t.Fatalf("expected AWS_REGION to be accepted: %v", err)
	}
}

func TestValidationRejectsInvalidAndPlatformOwnedEnvironment(t *testing.T) {
	for _, key := range []string{
		"A=B", "BAD KEY", "ÜNICODE", "HOME", "TMPDIR",
		"EZCLOUD_DATA_DIR", "EZCLOUD_CONFIG_DIR", "KERVIK_DATA_DIR",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := validateProfile(Profile{Name: "safe", EnvVars: []EnvVar{{Key: key, Value: "x"}}})
			if err == nil {
				t.Fatalf("accepted unsafe environment key %q", key)
			}
		})
	}
}

func TestValidationRejectsOversizedEnvironmentValue(t *testing.T) {
	_, err := validateProfile(Profile{
		Name:    "safe",
		EnvVars: []EnvVar{{Key: "AWS_CONFIG_FILE", Value: strings.Repeat("x", maxEnvValueBytes+1)}},
	})
	if err == nil {
		t.Fatal("accepted oversized environment value")
	}
}

func TestValidationAcceptsSafeEnvVars(t *testing.T) {
	root := tmpRoot(t)
	safe := []string{"AWS_PROFILE", "AWS_REGION", "AWS_CONFIG_FILE", "CUSTOM_VAR"}
	for _, key := range safe {
		t.Run(key, func(t *testing.T) {
			if _, err := Create(root, Profile{Name: "ok-" + key, EnvVars: []EnvVar{{Key: key, Value: "x"}}}); err != nil {
				t.Fatalf("expected %s to be accepted: %v", key, err)
			}
		})
	}
}

func TestEnvVarsDedupeByKeyLastWins(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a", EnvVars: []EnvVar{
		{Key: "REGION", Value: "first"},
		{Key: "REGION", Value: "second"},
	}})
	if len(created.EnvVars) != 1 || created.EnvVars[0].Value != "second" {
		t.Fatalf("env vars not deduped last-wins: %+v", created.EnvVars)
	}
}

func TestAccountsDedupe(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{
		{Provider: "aws", Account: "p"},
		{Provider: "aws", Account: "p"},
		{Provider: "gcp", Account: "q"},
	}})})
	accounts := GetCloudAccountsSettings(created).Accounts
	if len(accounts) != 2 {
		t.Fatalf("accounts not deduped: %+v", accounts)
	}
}

func TestCreateAcceptsNameWithSpacesAndUnicode(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "日本語 профиль 🚀 prod team"})
	if created.Name != "日本語 профиль 🚀 prod team" {
		t.Fatalf("name = %q", created.Name)
	}
	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != created.Name {
		t.Fatalf("name did not round-trip through disk: %q", got.Name)
	}
}

func TestCreateTrimsSurroundingWhitespaceFromName(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "  padded name  "})
	if created.Name != "padded name" {
		t.Fatalf("name = %q, want trimmed", created.Name)
	}
}

func TestRenameAcceptsUnicodeName(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "old"})
	if err := Rename(root, created.ID, "réseau été"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "réseau été" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestListSortedByNameCaseInsensitive(t *testing.T) {
	root := tmpRoot(t)
	for _, n := range []string{"zeta", "Alpha", "mid"} {
		mustCreate(t, root, Profile{Name: n})
	}
	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(list))
	for _, p := range list {
		got = append(got, p.Name)
	}
	if want := "Alpha,mid,zeta"; strings.Join(got, ",") != want {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
