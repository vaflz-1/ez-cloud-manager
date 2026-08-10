package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ez-cloud-manager/internal/plugin"
)

func TestFreshProfileHasZeroEnabledPlugins(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a"})
	if len(created.EnabledPlugins) != 0 {
		t.Fatalf("EnabledPlugins = %+v, want empty (empty-skeleton hub)", created.EnabledPlugins)
	}
	if created.Version != currentVersion {
		t.Fatalf("Version = %d, want %d (a fresh profile is never a legacy one)", created.Version, currentVersion)
	}
}

func TestEnabledPluginsPersistsAcrossSaveLoad(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a"})

	created.EnabledPlugins = []string{plugin.CloudAccountsID}
	if err := Save(root, created); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Get(root, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.EnabledPlugins) != 1 || loaded.EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("EnabledPlugins after reload = %+v", loaded.EnabledPlugins)
	}
}

func TestEnabledPluginsDedupeIsIdempotent(t *testing.T) {
	root := tmpRoot(t)
	// Enabling the same plugin twice (e.g. two rapid "enable" calls racing,
	// or a caller re-sending the same id) must never produce a duplicate —
	// same dedupe contract as normalizeAccounts/normalizeEnvVars.
	created := mustCreate(t, root, Profile{Name: "a", EnabledPlugins: []string{
		plugin.CloudAccountsID, plugin.CloudAccountsID,
	}})
	if len(created.EnabledPlugins) != 1 {
		t.Fatalf("EnabledPlugins not deduped: %+v", created.EnabledPlugins)
	}

	created.EnabledPlugins = append(created.EnabledPlugins, plugin.CloudAccountsID)
	if err := Save(root, created); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Get(root, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.EnabledPlugins) != 1 {
		t.Fatalf("EnabledPlugins not deduped after re-save: %+v", loaded.EnabledPlugins)
	}
}

func TestEnabledPluginsTrimsAndDropsBlank(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a", EnabledPlugins: []string{
		"  " + plugin.LaunchTemplatesID + "  ", "", "   ",
	}})
	if len(created.EnabledPlugins) != 1 || created.EnabledPlugins[0] != plugin.LaunchTemplatesID {
		t.Fatalf("EnabledPlugins = %+v", created.EnabledPlugins)
	}
}

func TestEnabledPluginsRejectsControlChars(t *testing.T) {
	root := tmpRoot(t)
	if _, err := Create(root, Profile{Name: "a", EnabledPlugins: []string{"bad\nid"}}); err == nil {
		t.Fatal("expected a control-character plugin id to be rejected")
	}
}

func TestEnabledPluginsCapEnforced(t *testing.T) {
	root := tmpRoot(t)
	ids := make([]string, maxEnabledPlugins+1)
	for i := range ids {
		ids[i] = "plugin-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	if _, err := Create(root, Profile{Name: "a", EnabledPlugins: ids}); err == nil {
		t.Fatal("expected exceeding maxEnabledPlugins to be rejected")
	}
}

func TestEnabledPluginsUnknownIDRoundTripsUnrejected(t *testing.T) {
	// internal/profile is deliberately registry-agnostic (see
	// normalizeEnabledPlugins's doc comment): a plugin id this build doesn't
	// know about must survive a save, not be silently stripped. Rejecting
	// unknown ids is the CLI/UI's job (plugin.ByID), not this package's.
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a", EnabledPlugins: []string{"some-future-plugin"}})
	if len(created.EnabledPlugins) != 1 || created.EnabledPlugins[0] != "some-future-plugin" {
		t.Fatalf("EnabledPlugins = %+v, want the unknown id preserved", created.EnabledPlugins)
	}
}

func TestUpdateEnabledPluginsIsTargetedBatchSave(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{
		Name:           "a",
		EnabledPlugins: []string{"some-future-plugin", plugin.TransferID},
		Settings: map[string]json.RawMessage{
			"some-future-plugin": json.RawMessage(`{"mode":"safe"}`),
		},
		WindowState: json.RawMessage(`{"selected":"catalog"}`),
	})
	created.UpdatedAt = "2000-01-01T00:00:00Z"
	writeRawProfile(t, root, created)

	saved, err := UpdateEnabledPlugins(root, created.ID, map[string]bool{
		plugin.TransferID:        false,
		plugin.CloudAccountsID:   true,
		plugin.LaunchTemplatesID: true,
	})
	if err != nil {
		t.Fatalf("UpdateEnabledPlugins: %v", err)
	}
	wantEnabled := []string{"some-future-plugin", plugin.CloudAccountsID, plugin.LaunchTemplatesID}
	if len(saved.EnabledPlugins) != len(wantEnabled) {
		t.Fatalf("enabledPlugins = %+v, want %+v", saved.EnabledPlugins, wantEnabled)
	}
	for i := range wantEnabled {
		if saved.EnabledPlugins[i] != wantEnabled[i] {
			t.Fatalf("enabledPlugins = %+v, want %+v", saved.EnabledPlugins, wantEnabled)
		}
	}
	if saved.UpdatedAt == created.UpdatedAt || saved.UpdatedAt == "" {
		t.Fatalf("updatedAt = %q, want a fresh batch-save timestamp", saved.UpdatedAt)
	}
	var settings struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(saved.Settings["some-future-plugin"], &settings); err != nil || settings.Mode != "safe" {
		t.Fatalf("settings were clobbered: %s (%v)", saved.Settings["some-future-plugin"], err)
	}
	var state struct {
		Selected string `json:"selected"`
	}
	if err := json.Unmarshal(saved.WindowState, &state); err != nil || state.Selected != "catalog" {
		t.Fatalf("window state was clobbered: %s (%v)", saved.WindowState, err)
	}
}

func TestEnabledPluginsPerProfileIndependence(t *testing.T) {
	root := tmpRoot(t)
	a := mustCreate(t, root, Profile{Name: "a", EnabledPlugins: []string{plugin.CloudAccountsID}})
	b := mustCreate(t, root, Profile{Name: "b", EnabledPlugins: []string{plugin.LaunchTemplatesID}})

	gotA, err := Get(root, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := Get(root, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA.EnabledPlugins) != 1 || gotA.EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("profile a EnabledPlugins = %+v", gotA.EnabledPlugins)
	}
	if len(gotB.EnabledPlugins) != 1 || gotB.EnabledPlugins[0] != plugin.LaunchTemplatesID {
		t.Fatalf("profile b EnabledPlugins = %+v", gotB.EnabledPlugins)
	}

	// Saving b must never touch a's file.
	gotB.EnabledPlugins = append(gotB.EnabledPlugins, plugin.TransferID)
	if err := Save(root, gotB); err != nil {
		t.Fatal(err)
	}
	reGotA, err := Get(root, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reGotA.EnabledPlugins) != 1 || reGotA.EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("profile a leaked b's plugin change: %+v", reGotA.EnabledPlugins)
	}
}

// writeRawProfile writes a profile.json by hand, bypassing Create/Save
// entirely — the only way to put a pre-P1 (Version 1) profile on disk, since
// every writer in this build now stamps currentVersion (2).
func writeRawProfile(t *testing.T, root string, p Profile) {
	t.Helper()
	dir := filepath.Join(root, p.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyVersionOneProfileAutoEnablesCloudAccountsInMemory(t *testing.T) {
	root := tmpRoot(t)
	id := "legacy0000000000000000000000000"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A genuine pre-P1.5 (here: pre-P1, Version 1) profile.json still has the
	// old top-level "accounts" key that the current Profile struct no longer
	// declares — write it by hand (json.Marshal(Profile{...}) can't produce
	// it any more) so this test exercises BOTH legacy migration blocks in
	// readProfile at once (Version<2 EnabledPlugins auto-enable AND Version<3
	// accounts-into-settings folding), the combination Risk #1 in the P1.5
	// plan calls out as worth covering together, not just in isolation.
	raw := `{"id":"` + id + `","name":"legacy","version":1,"accounts":[{"provider":"aws","account":"prod"}]}`
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Get(root, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.EnabledPlugins) != 1 || got.EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("EnabledPlugins = %+v, want [%s] auto-enabled for a pre-P1 profile", got.EnabledPlugins, plugin.CloudAccountsID)
	}
	accounts := GetCloudAccountsSettings(got).Accounts
	if len(accounts) != 1 || accounts[0] != (AccountRef{Provider: "aws", Account: "prod"}) {
		t.Fatalf("accounts = %+v, want the legacy top-level account folded into cloud-accounts settings", accounts)
	}

	// Both migrations are in-memory only — the file on disk must be untouched
	// until an explicit Save (this is what makes the mechanism self-erasing:
	// re-reading without saving must reproduce the exact same result).
	rawOnDisk, err := os.ReadFile(filepath.Join(dir, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Profile
	if err := json.Unmarshal(rawOnDisk, &onDisk); err != nil {
		t.Fatal(err)
	}
	if len(onDisk.EnabledPlugins) != 0 {
		t.Fatalf("on-disk EnabledPlugins = %+v, want untouched (still empty)", onDisk.EnabledPlugins)
	}
	if onDisk.Version != 1 {
		t.Fatalf("on-disk Version = %d, want still 1 (unmutated by a mere read)", onDisk.Version)
	}
	if len(onDisk.Settings) != 0 {
		t.Fatalf("on-disk Settings = %+v, want untouched (still absent)", onDisk.Settings)
	}
}

func TestLegacyProfileAutoEnableBecomesPermanentOnSave(t *testing.T) {
	root := tmpRoot(t)
	writeRawProfile(t, root, Profile{
		ID: "legacy1111111111111111111111111", Name: "legacy", Version: 1,
	})

	got, err := Get(root, "legacy1111111111111111111111111")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := Save(root, got); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "legacy1111111111111111111111111", "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Profile
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Version != currentVersion {
		t.Fatalf("on-disk Version = %d, want %d after Save", onDisk.Version, currentVersion)
	}
	if len(onDisk.EnabledPlugins) != 1 || onDisk.EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("on-disk EnabledPlugins = %+v, want the auto-enable to have stuck", onDisk.EnabledPlugins)
	}
}

func TestLegacyAutoEnableDoesNotDuplicateAnExplicitChoice(t *testing.T) {
	// A pre-P1 profile that (somehow — e.g. handcrafted, or a future
	// downgrade/upgrade cycle) already lists cloud-accounts must not end up
	// with it twice after the Version<2 auto-enable path runs.
	root := tmpRoot(t)
	writeRawProfile(t, root, Profile{
		ID: "legacy2222222222222222222222222", Name: "legacy", Version: 1,
		EnabledPlugins: []string{plugin.CloudAccountsID},
	})
	got, err := Get(root, "legacy2222222222222222222222222")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.EnabledPlugins) != 1 {
		t.Fatalf("EnabledPlugins = %+v, want no duplicate", got.EnabledPlugins)
	}
}

func TestListAlsoAppliesLegacyAutoEnable(t *testing.T) {
	// List() shares readProfile with Get(), so the same in-memory auto-enable
	// must show up there too — otherwise a profile picker built on List()
	// would disagree with Get() about whether the hub is empty.
	root := tmpRoot(t)
	writeRawProfile(t, root, Profile{
		ID: "legacy3333333333333333333333333", Name: "legacy", Version: 1,
	})
	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].EnabledPlugins) != 1 || list[0].EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("List() = %+v, want cloud-accounts auto-enabled", list)
	}
}
