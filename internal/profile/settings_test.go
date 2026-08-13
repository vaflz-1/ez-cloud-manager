package profile

import (
	"encoding/json"
	"strings"
	"testing"

	"ez-cloud-manager/internal/plugin"
)

// opaqueSettingsJSON builds a Profile.Settings map for a plugin id this
// package does not know the shape of — normalizeSettings must accept it as
// opaque, unvalidated JSON (see normalizeSettings's doc comment).
func opaqueSettingsJSON(t *testing.T, pluginID, rawJSON string) map[string]json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(rawJSON)) {
		t.Fatalf("test bug: %q is not valid JSON", rawJSON)
	}
	return map[string]json.RawMessage{pluginID: json.RawMessage(rawJSON)}
}

func TestSettingsCapEnforced(t *testing.T) {
	root := tmpRoot(t)
	blobs := make(map[string]json.RawMessage, maxSettingsBlobs+1)
	for i := 0; i < maxSettingsBlobs+1; i++ {
		blobs["plugin-"+string(rune('a'+i%26))+string(rune('0'+i/26))] = json.RawMessage(`{}`)
	}
	if _, err := Create(root, Profile{Name: "a", Settings: blobs}); err == nil {
		t.Fatal("expected exceeding maxSettingsBlobs to be rejected")
	}
}

func TestSettingsBlobSizeCapEnforced(t *testing.T) {
	root := tmpRoot(t)
	oversized := `{"data":"` + strings.Repeat("a", maxSettingsBlobBytes) + `"}`
	settings := opaqueSettingsJSON(t, "some-plugin", oversized)
	if _, err := Create(root, Profile{Name: "a", Settings: settings}); err == nil {
		t.Fatal("expected an oversized settings blob to be rejected")
	}
}

func TestSettingsBlobAtSizeCapIsAccepted(t *testing.T) {
	root := tmpRoot(t)
	// Pad so the *whole* raw JSON blob is exactly maxSettingsBlobBytes, not
	// over it — normalizeSettings must accept a blob at the boundary.
	padLen := maxSettingsBlobBytes - len(`{"data":""}`)
	blob := `{"data":"` + strings.Repeat("a", padLen) + `"}`
	if len(blob) != maxSettingsBlobBytes {
		t.Fatalf("test bug: built blob of %d bytes, want exactly %d", len(blob), maxSettingsBlobBytes)
	}
	settings := opaqueSettingsJSON(t, "some-plugin", blob)
	if _, err := Create(root, Profile{Name: "a", Settings: settings}); err != nil {
		t.Fatalf("expected a blob at exactly the byte cap to be accepted: %v", err)
	}
}

func TestSettingsRejectsInvalidJSON(t *testing.T) {
	root := tmpRoot(t)
	settings := map[string]json.RawMessage{"some-plugin": json.RawMessage(`not-json-at-all`)}
	if _, err := Create(root, Profile{Name: "a", Settings: settings}); err == nil {
		t.Fatal("expected invalid JSON in a settings blob to be rejected")
	}
}

func TestSettingsRejectsControlCharKey(t *testing.T) {
	root := tmpRoot(t)
	settings := map[string]json.RawMessage{"bad\nkey": json.RawMessage(`{}`)}
	if _, err := Create(root, Profile{Name: "a", Settings: settings}); err == nil {
		t.Fatal("expected a control-character settings key to be rejected")
	}
}

func TestSettingsRejectsEmptyKey(t *testing.T) {
	root := tmpRoot(t)
	settings := map[string]json.RawMessage{"": json.RawMessage(`{}`)}
	if _, err := Create(root, Profile{Name: "a", Settings: settings}); err == nil {
		t.Fatal("expected an empty settings key to be rejected")
	}
}

// TestSettingsOpaqueUnknownPluginPassesThroughUnvalidated is the P1.5
// counterpart of normalizeEnabledPlugins's "unknown ids round-trip
// unrejected" guarantee: internal/profile validates ONLY the one key it
// knows the shape of (cloud-accounts); every other plugin's blob must
// survive a Create/Save byte-for-byte (modulo JSON re-marshaling), including
// arbitrary nested structure a future/foreign plugin might store.
func TestSettingsOpaqueUnknownPluginPassesThroughUnvalidated(t *testing.T) {
	root := tmpRoot(t)
	raw := `{"foo":"bar","nested":{"x":1,"y":[1,2,3]},"empty":""}`
	created := mustCreate(t, root, Profile{Name: "a", Settings: opaqueSettingsJSON(t, "some-future-plugin", raw)})

	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	blob := GetSettingsBlob(got, "some-future-plugin")
	var decoded map[string]any
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("stored blob is not valid JSON: %v", err)
	}
	if decoded["foo"] != "bar" {
		t.Fatalf("opaque blob content changed: %+v", decoded)
	}
}

func TestGetSettingsBlobReturnsNilWhenAbsent(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a"})
	if blob := GetSettingsBlob(created, "no-such-plugin"); blob != nil {
		t.Fatalf("expected nil for an absent plugin id, got %s", blob)
	}
}

func TestSetSettingsBlobGetRoundTrip(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a"})

	raw := json.RawMessage(`{"foo":"bar"}`)
	saved, err := SetSettingsBlob(root, created.ID, "some-plugin", raw)
	if err != nil {
		t.Fatalf("SetSettingsBlob: %v", err)
	}
	blob := GetSettingsBlob(saved, "some-plugin")
	var decoded map[string]string
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["foo"] != "bar" {
		t.Fatalf("blob = %s, want foo=bar", blob)
	}

	// And it round-trips through a fresh Get too, not just the return value.
	reloaded, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(GetSettingsBlob(reloaded, "some-plugin")) != string(blob) {
		t.Fatalf("blob did not persist: %s", GetSettingsBlob(reloaded, "some-plugin"))
	}
}

func TestSetSettingsBlobPreservesOtherPluginsBlobs(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "a", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{
		Accounts: []AccountRef{{Provider: "aws", Account: "prod"}},
	})})

	if _, err := SetSettingsBlob(root, created.ID, "unrelated-plugin", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatalf("SetSettingsBlob: %v", err)
	}

	got, err := Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	accounts := GetCloudAccountsSettings(got).Accounts
	if len(accounts) != 1 || accounts[0] != (AccountRef{Provider: "aws", Account: "prod"}) {
		t.Fatalf("cloud-accounts settings clobbered by an unrelated SetSettingsBlob: %+v", accounts)
	}
}

func TestSetSettingsBlobUnknownProfileFails(t *testing.T) {
	root := tmpRoot(t)
	if _, err := SetSettingsBlob(root, "does-not-exist", "some-plugin", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for a nonexistent profile id")
	}
}

func TestGetCloudAccountsSettingsZeroValueOnMalformedBlob(t *testing.T) {
	// Persisted profiles now re-run validateProfile on every read and therefore
	// reject this tampering. Keep the helper's defensive contract pinned with an
	// in-memory foreign Profile: callers still get a zero value, never a panic.
	foreign := Profile{Settings: map[string]json.RawMessage{
		plugin.CloudAccountsID: json.RawMessage(`{"accounts":"not-an-array"}`),
	}}
	s := GetCloudAccountsSettings(foreign)
	if s.ShowAllAccounts || len(s.Accounts) != 0 {
		t.Fatalf("expected the zero value for a malformed blob, got %+v", s)
	}
}

// TestDuplicateSettingsAreIndependentCopies guards cloneSettings: mutating
// the duplicate's Settings map (as the CLI/UI does before a later Save) must
// never alias, and thus never corrupt, the source profile's own map.
func TestDuplicateSettingsAreIndependentCopies(t *testing.T) {
	root := tmpRoot(t)
	src := mustCreate(t, root, Profile{Name: "prod", Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{
		Accounts: []AccountRef{{Provider: "aws", Account: "prod"}},
	})})

	dup, err := Duplicate(root, src.ID, "prod-copy")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}

	dup.Settings = cloneSettings(dup.Settings)
	dup.Settings["cloud-accounts"] = json.RawMessage(`{"showAllAccounts":true,"accounts":[]}`)
	if err := Save(root, dup); err != nil {
		t.Fatalf("save dup: %v", err)
	}

	reloadedSrc, err := Get(root, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	srcAccounts := GetCloudAccountsSettings(reloadedSrc).Accounts
	if len(srcAccounts) != 1 || srcAccounts[0] != (AccountRef{Provider: "aws", Account: "prod"}) {
		t.Fatalf("source profile's settings were mutated via the duplicate's map: %+v", srcAccounts)
	}
}
