package gcpcreds

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ez-cloud-manager/internal/inifile"
)

func TestSaveGetListRoundtrip(t *testing.T) {
	root := t.TempDir()

	fields := map[string]string{
		KeyAccount: "dev@example.com",
		KeyProject: "proj-1",
		KeyRegion:  "europe-west1",
		KeyZone:    "europe-west1-b",
	}
	if err := Save(root, "work", fields); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Get(root, "work")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for key, want := range fields {
		if got.Fields[key] != want {
			t.Errorf("field %s = %q, want %q", key, got.Fields[key], want)
		}
	}

	// File must be gcloud-native: sections, not dotted keys.
	data, err := os.ReadFile(filepath.Join(root, "configurations", "config_work"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	content := string(data)
	for _, want := range []string{"[core]", "[compute]", "project = proj-1", "region = europe-west1"} {
		if !strings.Contains(content, want) {
			t.Errorf("config file missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "core.project") {
		t.Error("dotted keys leaked into gcloud file")
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "work" {
		t.Fatalf("list = %+v", list)
	}
}

func TestListMissingRootIsEmpty(t *testing.T) {
	list, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %+v", list)
	}
}

func TestSaveRejectsBadNames(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"", "Work", "has space", "-lead", "../escape", "a/b"} {
		if err := Save(root, bad, map[string]string{KeyProject: "p"}); err == nil {
			t.Errorf("expected error for name %q", bad)
		}
	}
}

func TestGetAndDeleteRejectPathTraversal(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "sentinel")
	const original = "[core]\nproject = must-survive\n"
	if err := os.WriteFile(sentinel, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Before name validation, config_ + this value cleaned to root/sentinel.
	attack := "x/../../sentinel"
	if _, err := Get(root, attack); err == nil {
		t.Fatal("Get accepted a traversal configuration name")
	}
	if err := Delete(root, attack); err == nil {
		t.Fatal("Delete accepted a traversal configuration name")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel was removed: %v", err)
	}
	if string(data) != original {
		t.Fatalf("sentinel changed: %q", data)
	}
}

func TestSaveRejectsUnsafeSectionAndPropertyNames(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, "safe", map[string]string{KeyProject: "original"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "configurations", "config_safe")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"core]\n[injected.value",
		"bad section.value",
		"core.bad\nvalue",
		".value",
		"core.",
	} {
		t.Run(strings.ReplaceAll(key, "\n", "\\n"), func(t *testing.T) {
			if err := Save(root, "safe", map[string]string{key: "payload"}); err == nil {
				t.Fatalf("Save accepted unsafe property key %q", key)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(original) {
				t.Fatalf("rejected save changed configuration for key %q", key)
			}
		})
	}
}

func TestBareKeyGoesToCore(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, "cfg", map[string]string{"project": "bare"}); err != nil {
		t.Fatal(err)
	}
	got, _ := Get(root, "cfg")
	if got.Fields[KeyProject] != "bare" {
		t.Errorf("bare key not normalized to core.project: %+v", got.Fields)
	}
}

func TestActivateAndDeleteGuard(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, "one", map[string]string{KeyProject: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, "two", map[string]string{KeyProject: "p2"}); err != nil {
		t.Fatal(err)
	}

	if err := Activate(root, "one"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := ActiveName(root); got != "one" {
		t.Fatalf("active = %q, want one", got)
	}

	if err := Delete(root, "one"); err == nil {
		t.Fatal("deleting the active configuration must be refused")
	}
	if err := Delete(root, "two"); err != nil {
		t.Fatalf("delete inactive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "configurations", "config_two")); !os.IsNotExist(err) {
		t.Error("config_two should be gone")
	}
	matches, _ := filepath.Glob(filepath.Join(root, "configurations", "config_two.bak.*"))
	if len(matches) == 0 {
		t.Error("expected a backup before delete")
	}

	if err := Activate(root, "ghost"); err == nil {
		t.Error("activating a missing configuration must fail")
	}
}

// TestListExcludesDeleteBackups guards against a real bug found in review:
// Delete leaves a "config_<name>.bak.<timestamp>" file next to the real
// configs (see TestActivateAndDeleteGuard above), and List's old prefix-cut
// logic let that backup through as if it were an ordinary, selectable
// configuration named "<name>.bak.<timestamp>" — including in the guided
// gcloud-delete picker, where picking it would Activate a bogus name. List
// must only ever return names that satisfy nameRe (gcloud's own naming
// rule), which backup timestamps never do.
func TestListExcludesDeleteBackups(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, "keep", map[string]string{KeyProject: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, "gone", map[string]string{KeyProject: "p2"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(root, "gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "configurations", "config_gone.bak.*"))
	if len(matches) == 0 {
		t.Fatal("expected a backup file on disk to test against")
	}

	profiles, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, p := range profiles {
		if strings.Contains(p.Name, ".bak.") {
			t.Fatalf("List returned a backup file as a configuration: %+v", profiles)
		}
	}
	if len(profiles) != 1 || profiles[0].Name != "keep" {
		t.Fatalf("profiles = %+v, want only [keep]", profiles)
	}
}

func TestParseGcloudConfigList(t *testing.T) {
	text := `[core]
account = me@example.com
project = my-proj
[compute]
region = us-east1
zone = us-east1-b`
	parsed := Parse(text)
	want := map[string]string{
		KeyAccount: "me@example.com",
		KeyProject: "my-proj",
		KeyRegion:  "us-east1",
		KeyZone:    "us-east1-b",
	}
	for key, value := range want {
		if parsed.Fields[key] != value {
			t.Errorf("field %s = %q, want %q", key, parsed.Fields[key], value)
		}
	}
}

func TestParseEnvVariants(t *testing.T) {
	cases := []struct {
		text string
		key  string
		want string
	}{
		{"export CLOUDSDK_CORE_PROJECT=p1", KeyProject, "p1"},
		{"CLOUDSDK_COMPUTE_ZONE=us-west1-a", KeyZone, "us-west1-a"},
		{"GOOGLE_CLOUD_PROJECT=p2", KeyProject, "p2"},
		{"GOOGLE_APPLICATION_CREDENTIALS=/keys/sa.json", KeyCredFile, "/keys/sa.json"},
		{"core.project = dotted", KeyProject, "dotted"},
	}
	for _, tc := range cases {
		parsed := Parse(tc.text)
		if parsed.Fields[tc.key] != tc.want {
			t.Errorf("Parse(%q)[%s] = %q, want %q", tc.text, tc.key, parsed.Fields[tc.key], tc.want)
		}
	}

	parsed := Parse("export CLOUDSDK_ACTIVE_CONFIG_NAME=staging\nCLOUDSDK_CORE_PROJECT=p")
	if parsed.ProfileName != "staging" {
		t.Errorf("profile name = %q, want staging", parsed.ProfileName)
	}
}

func TestParseServiceAccountJSONNeverStoresPrivateKey(t *testing.T) {
	text := `{
	  "type": "service_account",
	  "project_id": "sa-proj",
	  "private_key_id": "kid",
	  "private_key": "-----BEGIN PRIVATE KEY-----\nMII…\n-----END PRIVATE KEY-----\n",
	  "client_email": "robot@sa-proj.iam.gserviceaccount.com"
	}`
	parsed := Parse(text)
	if parsed.Fields[KeyProject] != "sa-proj" {
		t.Errorf("project = %q", parsed.Fields[KeyProject])
	}
	if parsed.Fields[KeyAccount] != "robot@sa-proj.iam.gserviceaccount.com" {
		t.Errorf("account = %q", parsed.Fields[KeyAccount])
	}
	for key, value := range parsed.Fields {
		if strings.Contains(value, "PRIVATE KEY") {
			t.Errorf("private key material leaked into field %s", key)
		}
	}
	if len(parsed.Notes) == 0 {
		t.Error("expected an explanatory note about the ignored private key")
	}
}

func TestSavePreservesUnknownPropertiesAndComments(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "configurations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := "# gcloud wrote this\n[core]\naccount = old@example.com\ncustom_prop = keep-me\n"
	if err := os.WriteFile(filepath.Join(dir, "config_seeded"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Save(root, "seeded", map[string]string{KeyAccount: "new@example.com"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config_seeded"))
	content := string(data)
	if !strings.Contains(content, "custom_prop = keep-me") {
		t.Error("unknown property was lost on save")
	}
	if !strings.Contains(content, "# gcloud wrote this") {
		t.Error("comment was lost on save")
	}
	if !strings.Contains(content, "new@example.com") {
		t.Error("edit was not applied")
	}
}

func TestConditionalBatchRollsBackEarlierWriteWhenLaterWriteFails(t *testing.T) {
	root := t.TempDir()
	one := map[string]string{KeyAccount: "old-one@example.com", KeyProject: "project-one"}
	two := map[string]string{KeyAccount: "old-two@example.com", KeyProject: "project-two"}
	if err := Save(root, "project-one", one); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, "project-two", two); err != nil {
		t.Fatal(err)
	}

	originalWriter := writeBatchAtomic
	calls := 0
	writeBatchAtomic = func(path string, model inifile.Model, backup bool) error {
		calls++
		if calls == 2 {
			return errors.New("injected second-row write failure")
		}
		return inifile.WriteAtomic(path, model, backup)
	}
	t.Cleanup(func() { writeBatchAtomic = originalWriter })

	err := SaveBatchIfUnchanged(root, []ConditionalSave{
		{Name: "project-one", Fields: map[string]string{KeyAccount: "new@example.com", KeyProject: "project-one"}, ExpectedFields: one},
		{Name: "project-two", Fields: map[string]string{KeyAccount: "new@example.com", KeyProject: "project-two"}, ExpectedFields: two},
	})
	if err == nil || !strings.Contains(err.Error(), "injected second-row") {
		t.Fatalf("expected injected failure, got %v", err)
	}
	for name, want := range map[string]map[string]string{"project-one": one, "project-two": two} {
		got, getErr := Get(root, name)
		if getErr != nil {
			t.Fatal(getErr)
		}
		for key, value := range want {
			if got.Fields[key] != value {
				t.Fatalf("%s was left partially changed: %+v", name, got.Fields)
			}
		}
	}
}

func TestGetAndConditionalSaveRejectDuplicateIdentitySections(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "configurations", "config_ambiguous")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "[core]\naccount = first@example.com\nproject = project-one\n\n[core]\naccount = second@example.com\nproject = project-two\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(root, "ambiguous"); err == nil || !strings.Contains(err.Error(), "duplicate section") {
		t.Fatalf("ambiguous identity was accepted: %v", err)
	}
	if err := SaveIfUnchanged(root, "ambiguous", map[string]string{KeyAccount: "new@example.com"}, map[string]string{}, false); err == nil {
		t.Fatal("conditional save accepted duplicate identity sections")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("rejected ambiguous config changed on disk: %q", after)
	}
}

func TestGetRejectsDuplicateIdentityProperty(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "configurations", "config_ambiguous")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[core]\naccount = first@example.com\naccount = second@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(root, "ambiguous"); err == nil || !strings.Contains(err.Error(), "duplicate property") {
		t.Fatalf("duplicate identity property was accepted: %v", err)
	}
}
