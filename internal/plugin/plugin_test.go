package plugin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuiltins(t *testing.T) {
	all := Builtins()
	wantCategory := map[string]Category{
		CloudAccountsID:   CategoryDevOps,
		LaunchTemplatesID: CategoryDevOps,
		TransferID:        CategoryDevOps,
	}
	if len(all) != len(wantCategory) {
		t.Fatalf("Builtins() returned %d manifests, want %d", len(all), len(wantCategory))
	}
	seen := make(map[string]bool, len(all))
	for _, m := range all {
		cat, ok := wantCategory[m.ID]
		if !ok {
			t.Errorf("unexpected builtin id %q", m.ID)
			continue
		}
		seen[m.ID] = true
		if m.Category != cat {
			t.Errorf("%s: category = %q, want %q", m.ID, m.Category, cat)
		}
		if m.Runtime != "builtin" {
			t.Errorf("%s: runtime = %q, want %q", m.ID, m.Runtime, "builtin")
		}
		if m.Name == "" || m.Description == "" || m.Icon == "" {
			t.Errorf("%s: missing display metadata: %+v", m.ID, m)
		}
	}
	for id := range wantCategory {
		if !seen[id] {
			t.Errorf("expected builtin %q not found", id)
		}
	}
}

func TestDescriptorProjection(t *testing.T) {
	for _, m := range Builtins() {
		d := m.Descriptor()
		want := Descriptor{ID: m.ID, Name: m.Name, Description: m.Description, Icon: m.Icon, Clouds: m.Clouds, Category: m.Category}
		if !reflect.DeepEqual(d, want) {
			t.Errorf("Descriptor() = %+v, want %+v", d, want)
		}
	}
	ds := Descriptors()
	if len(ds) != len(Builtins()) {
		t.Fatalf("Descriptors() length = %d, want %d", len(ds), len(Builtins()))
	}
}

func TestByID(t *testing.T) {
	if _, ok := ByID("does-not-exist"); ok {
		t.Fatal("ByID should return false for an unknown id")
	}
	m, ok := ByID(CloudAccountsID)
	if !ok || m.ID != CloudAccountsID {
		t.Fatalf("ByID(%q) = %+v, %v", CloudAccountsID, m, ok)
	}
}

// TestManifestMatchesDocExample keeps Manifest's JSON shape honest against
// docs/PLATFORM.md's own "Manifest & contribution points" example, with
// Name/Description/Icon/Category added — the disclosed P1 extension beyond
// that example (see plugin.go's package doc).
func TestManifestMatchesDocExample(t *testing.T) {
	const docExample = `{
  "id": "ec2-launch-templates",
  "version": "2.0.0",
  "publisher": "ezcloud",
  "engines": { "ezcloud": ">=2.0" },
  "clouds": ["aws"],
  "runtime": "declarative",
  "name": "Launch Templates",
  "description": "Edit EC2 launch templates like plain configs.",
  "icon": "server.rack",
  "category": "DevOps",
  "permissions": {
    "cli": ["aws ec2 describe-launch-template*", "aws ec2 create-launch-template*", "aws ec2 delete-launch-template"],
    "env": ["AWS_PROFILE", "AWS_REGION"],
    "network": "none"
  },
  "contributes": {
    "menus":    [{ "menu": "cloud/aws", "label": "Launch Templates", "command": "lt.open" }],
    "sidebar":  [{ "section": "AWS", "id": "lt.tree", "view": "views/lt-tree.yaml" }],
    "views":    [{ "id": "lt.table", "spec": "views/lt-table.yaml" }],
    "settings": [{ "pane": "Launch Templates", "schema": "settings.schema.json" }],
    "commands": [{ "id": "lt.copyEdit", "title": "Duplicate & Edit…",
                   "exec": "aws ec2 create-launch-template --cli-input-json {payload}" }],
    "resources": [{ "type": "aws/launch-template",
                    "list": "aws ec2 describe-launch-templates --output json",
                    "bind": "$.LaunchTemplates[*]",
                    "verbs": ["create", "delete", "duplicate-edit"] }]
  }
}`
	var m Manifest
	if err := json.Unmarshal([]byte(docExample), &m); err != nil {
		t.Fatalf("unmarshal doc example: %v", err)
	}
	if m.ID != "ec2-launch-templates" || m.Runtime != "declarative" || m.Category != CategoryDevOps {
		t.Fatalf("unexpected decode: %+v", m)
	}
	if len(m.Contributes.Menus) != 1 || m.Contributes.Menus[0].Command != "lt.open" {
		t.Fatalf("menus contribution not decoded: %+v", m.Contributes.Menus)
	}
	if len(m.Permissions.CLI) != 3 {
		t.Fatalf("permissions.cli not decoded: %+v", m.Permissions.CLI)
	}
	if len(m.Contributes.Resources) != 1 || m.Contributes.Resources[0].Bind != "$.LaunchTemplates[*]" {
		t.Fatalf("resources contribution not decoded: %+v", m.Contributes.Resources)
	}

	// Round-trip through JSON must be stable — keeps the struct honest
	// against future doc drift.
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Manifest
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if !reflect.DeepEqual(m, again) {
		t.Fatalf("round-trip mismatch:\nfirst:  %+v\nsecond: %+v", m, again)
	}
}
