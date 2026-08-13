package profile

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"ez-cloud-manager/internal/plugin"
)

func TestExportImportRoundtrip(t *testing.T) {
	srcRoot := tmpRoot(t)
	src := mustCreate(t, srcRoot, Profile{
		Name:     "prod",
		Settings: cloudAccountsSettingsJSON(t, CloudAccountsSettings{Accounts: []AccountRef{{Provider: "aws", Account: "default"}}}),
		EnvVars:  []EnvVar{{Key: "REGION", Value: "us-east-1"}},
	})

	var buf bytes.Buffer
	if err := Export(srcRoot, src.ID, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	dstRoot := tmpRoot(t)
	imported, err := Import(dstRoot, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.Name != "prod" {
		t.Fatalf("name = %q", imported.Name)
	}
	if imported.ID == src.ID {
		t.Fatal("import must assign a fresh id, not reuse the exported one")
	}
	importedAccounts := GetCloudAccountsSettings(imported).Accounts
	if len(importedAccounts) != 0 || GetCloudAccountsSettings(imported).ShowAllAccounts {
		t.Fatalf("import inherited machine-specific Connection grants: %+v", importedAccounts)
	}
	if len(imported.EnvVars) != 1 || imported.EnvVars[0] != src.EnvVars[0] {
		t.Fatalf("env vars not preserved: %+v", imported.EnvVars)
	}
}

func TestExportImportResetsShowAllAccountsAndPreservesEnabledPlugins(t *testing.T) {
	srcRoot := tmpRoot(t)
	src := mustCreate(t, srcRoot, Profile{
		Name:           "everything",
		Settings:       cloudAccountsSettingsJSON(t, CloudAccountsSettings{ShowAllAccounts: true}),
		EnabledPlugins: []string{"ec2-launch-templates"},
	})

	var buf bytes.Buffer
	if err := Export(srcRoot, src.ID, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	dstRoot := tmpRoot(t)
	imported, err := Import(dstRoot, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if GetCloudAccountsSettings(imported).ShowAllAccounts {
		t.Fatal("showAllAccounts must reset on import so local Connections require explicit review")
	}
	if len(imported.EnabledPlugins) != 1 || imported.EnabledPlugins[0] != "ec2-launch-templates" {
		t.Fatalf("enabledPlugins = %+v", imported.EnabledPlugins)
	}
}

func TestImportIgnoresForgedIDVersionAndTimestamps(t *testing.T) {
	root := tmpRoot(t)
	var buf bytes.Buffer
	forged := `{"id":"forged-id","name":"prod","version":999,"createdAt":"1999-01-01T00:00:00Z","updatedAt":"1999-01-01T00:00:00Z","accounts":[],"envVars":[]}`
	if err := writeZip(&buf, map[string]string{"profile.json": forged}); err != nil {
		t.Fatal(err)
	}

	imported, err := Import(root, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.ID == "forged-id" {
		t.Fatal("import must not adopt an imported id")
	}
	if imported.Version != currentVersion {
		t.Fatalf("version = %d, want %d (not the forged 999)", imported.Version, currentVersion)
	}
	if imported.CreatedAt == "1999-01-01T00:00:00Z" {
		t.Fatal("import must stamp its own createdAt, not reuse the forged one")
	}
}

func TestImportV4WithoutConnectionPolicyIsExplicitAllowNone(t *testing.T) {
	root := tmpRoot(t)
	var buf bytes.Buffer
	if err := writeZip(&buf, map[string]string{
		"profile.json": `{"name":"legacy-v4","version":4}`,
	}); err != nil {
		t.Fatal(err)
	}

	imported, err := Import(root, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	settings := GetCloudAccountsSettings(imported)
	if settings.ShowAllAccounts || len(settings.Accounts) != 0 {
		t.Fatalf("imported v4 policy = %+v, want explicit allow-none", settings)
	}
	if _, exists := imported.Settings[plugin.CloudAccountsID]; !exists {
		t.Fatal("imported v4 profile omitted materialized Connection policy")
	}
	if AllowsConnection(imported, AccountRef{Provider: "aws", Account: "default"}) {
		t.Fatal("imported v4 profile retained missing-policy show-all behavior")
	}
}

func TestImportNameCollisionAppendsSuffix(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "prod"})

	var buf bytes.Buffer
	if err := writeZip(&buf, map[string]string{"profile.json": `{"name":"prod","accounts":[],"envVars":[]}`}); err != nil {
		t.Fatal(err)
	}

	imported, err := Import(root, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.Name != "prod (Imported)" {
		t.Fatalf("name = %q, want %q", imported.Name, "prod (Imported)")
	}

	var buf2 bytes.Buffer
	if err := writeZip(&buf2, map[string]string{"profile.json": `{"name":"prod","accounts":[],"envVars":[]}`}); err != nil {
		t.Fatal(err)
	}
	imported2, err := Import(root, bytes.NewReader(buf2.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported2.Name != "prod (Imported 2)" {
		t.Fatalf("name = %q, want %q", imported2.Name, "prod (Imported 2)")
	}
}

func TestImportRejectsPathTraversal(t *testing.T) {
	root := tmpRoot(t)
	var buf bytes.Buffer
	if err := writeZip(&buf, map[string]string{"../../etc/profile.json": `{"name":"evil"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(root, bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected error importing a zip with a path-traversal entry")
	}
}

func TestImportRejectsUnknownEntry(t *testing.T) {
	root := tmpRoot(t)
	var buf bytes.Buffer
	if err := writeZip(&buf, map[string]string{"payload.exe": "not a profile"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(root, bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected error importing a zip with a non-allowlisted entry")
	}
}

func TestImportRejectsOversizedInput(t *testing.T) {
	root := tmpRoot(t)
	huge := strings.Repeat("a", maxImportBytes+1)
	if _, err := Import(root, strings.NewReader(huge)); err == nil {
		t.Fatal("expected error importing an oversized input")
	}
}

func TestImportRejectsMissingProfileJSON(t *testing.T) {
	root := tmpRoot(t)
	var buf bytes.Buffer
	// An empty, valid zip has no entries at all.
	if err := writeZip(&buf, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(root, bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected error importing a zip without profile.json")
	}
}

func TestImportRejectsMalformedZip(t *testing.T) {
	root := tmpRoot(t)
	if _, err := Import(root, strings.NewReader("not a zip file")); err == nil {
		t.Fatal("expected error importing a non-zip file")
	}
}

// writeZip is a small test helper building an in-memory zip from name→content.
func writeZip(w *bytes.Buffer, files map[string]string) error {
	zw := zip.NewWriter(w)
	for name, content := range files {
		entry, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			return err
		}
	}
	return zw.Close()
}
