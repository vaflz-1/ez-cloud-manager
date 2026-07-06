package azurecreds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "azure_profiles.ini")
}

func TestSaveGetListDeleteRoundtrip(t *testing.T) {
	path := testPath(t)

	fields := map[string]string{
		KeyTenantID:       "11111111-1111-1111-1111-111111111111",
		KeyClientID:       "22222222-2222-2222-2222-222222222222",
		KeyClientSecret:   "s3cr3t~value",
		KeySubscriptionID: "33333333-3333-3333-3333-333333333333",
	}
	if err := Save(path, "client-a", fields); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := Save(path, "client-b", map[string]string{KeyTenantID: "t2"}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	got, err := Get(path, "client-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for key, want := range fields {
		if got.Fields[key] != want {
			t.Errorf("field %s = %q, want %q", key, got.Fields[key], want)
		}
	}

	list, err := List(path)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}

	if err := Delete(path, "client-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = List(path)
	if len(list) != 1 || list[0].Name != "client-b" {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestSaveSetsRestrictivePermissions(t *testing.T) {
	path := testPath(t)
	if err := Save(path, "p", map[string]string{KeyClientSecret: "top-secret"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestSaveRejectsInjection(t *testing.T) {
	path := testPath(t)
	if err := Save(path, "bad[name]", map[string]string{KeyTenantID: "x"}); err == nil {
		t.Error("expected error for section-injection profile name")
	}
	if err := Save(path, "ok", map[string]string{KeyTenantID: "x\ny=1"}); err == nil {
		t.Error("expected error for newline in value")
	}
}

func TestParseSpCreateForRbacJSON(t *testing.T) {
	text := `{
	  "appId": "aaaa-bbbb",
	  "displayName": "my-sp",
	  "password": "p@ss/w0rd",
	  "tenant": "tttt-uuuu"
	}`
	parsed := Parse(text)
	if parsed.ProfileName != "my-sp" {
		t.Errorf("profile name = %q, want my-sp", parsed.ProfileName)
	}
	want := map[string]string{
		KeyClientID:     "aaaa-bbbb",
		KeyClientSecret: "p@ss/w0rd",
		KeyTenantID:     "tttt-uuuu",
	}
	for key, value := range want {
		if parsed.Fields[key] != value {
			t.Errorf("field %s = %q, want %q", key, parsed.Fields[key], value)
		}
	}
	if len(parsed.Notes) == 0 {
		t.Error("expected a note about the captured client secret")
	}
}

func TestParseEnvLines(t *testing.T) {
	cases := []struct {
		name string
		text string
		key  string
		want string
	}{
		{"bash export", "export AZURE_TENANT_ID=abc", KeyTenantID, "abc"},
		{"terraform ARM_", "ARM_CLIENT_SECRET='q u o t e d'", KeyClientSecret, "q u o t e d"},
		{"powershell", `$env:AZURE_SUBSCRIPTION_ID = "sub-1"`, KeySubscriptionID, "sub-1"},
		{"plain known key", "subscription_id=sub-2", KeySubscriptionID, "sub-2"},
		{"arm environment", "ARM_ENVIRONMENT=AzureUSGovernment", KeyCloud, "AzureUSGovernment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := Parse(tc.text)
			if parsed.Fields[tc.key] != tc.want {
				t.Errorf("Parse(%q)[%s] = %q, want %q", tc.text, tc.key, parsed.Fields[tc.key], tc.want)
			}
		})
	}
}

func TestParseIgnoresUnknownEnvAndJSONKeys(t *testing.T) {
	parsed := Parse("export RANDOM_VAR=1\nexport AZURE_CLIENT_ID=cid")
	if _, ok := parsed.Fields["random_var"]; ok {
		t.Error("unknown env var must not be imported")
	}
	if parsed.Fields[KeyClientID] != "cid" {
		t.Error("known env var missing")
	}

	parsed = Parse(`{"appId":"a","unknownThing":"zzz"}`)
	for key := range parsed.Fields {
		if key == "unknownthing" {
			t.Error("unknown JSON key must not be imported")
		}
	}
}

func TestBackupCreatedOnRewrite(t *testing.T) {
	path := testPath(t)
	if err := Save(path, "p", map[string]string{KeyTenantID: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, "p", map[string]string{KeyTenantID: "2"}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Error("expected a timestamped backup after rewrite")
	}
}

func TestEnvKeyNormalizationOnSave(t *testing.T) {
	path := testPath(t)
	if err := Save(path, "p", map[string]string{"AZURE_TENANT_ID": "via-env-name"}); err != nil {
		t.Fatal(err)
	}
	got, _ := Get(path, "p")
	if got.Fields[KeyTenantID] != "via-env-name" {
		t.Errorf("env-name key not normalized: %+v", got.Fields)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "AZURE_TENANT_ID") {
		t.Error("file should store normalized lowercase keys")
	}
}
