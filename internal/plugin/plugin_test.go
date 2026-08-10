package plugin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuiltins(t *testing.T) {
	all := Builtins()
	wantOrder := []string{CloudAccountsID, LaunchTemplatesID, TransferID}
	wantCategory := map[string]Category{
		CloudAccountsID:   CategoryDevOps,
		LaunchTemplatesID: CategoryDevOps,
		TransferID:        CategoryDevOps,
	}
	if len(all) != len(wantCategory) {
		t.Fatalf("Builtins() returned %d manifests, want %d", len(all), len(wantCategory))
	}
	seen := make(map[string]bool, len(all))
	for i, m := range all {
		if m.ID != wantOrder[i] {
			t.Errorf("Builtins()[%d].ID = %q, want stable order entry %q", i, m.ID, wantOrder[i])
		}
		cat, ok := wantCategory[m.ID]
		if !ok {
			t.Errorf("unexpected builtin id %q", m.ID)
			continue
		}
		seen[m.ID] = true
		if m.Category != cat {
			t.Errorf("%s: category = %q, want %q", m.ID, m.Category, cat)
		}
		if m.Runtime.Type != "builtin" {
			t.Errorf("%s: runtime.type = %q, want %q", m.ID, m.Runtime.Type, "builtin")
		}
		if m.ID == CloudAccountsID && m.Kind != KindSystem {
			t.Errorf("%s: kind = %q, want %q", m.ID, m.Kind, KindSystem)
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

func TestBuiltinsDisplayMetadataComesFromEmbeddedManifests(t *testing.T) {
	wantNames := map[string]string{
		CloudAccountsID:   "Connections",
		LaunchTemplatesID: "Launch Templates",
		TransferID:        "Transfer",
	}
	for _, manifest := range Builtins() {
		if manifest.Name != wantNames[manifest.ID] {
			t.Errorf("%s name = %q, want %q", manifest.ID, manifest.Name, wantNames[manifest.ID])
		}
		if manifest.Schema != "../addon.schema.json" {
			t.Errorf("%s schema = %q, want package-relative schema", manifest.ID, manifest.Schema)
		}
	}
}

func TestBuiltinsReturnsIndependentCopies(t *testing.T) {
	first := Builtins()
	first[0].Requires.Connectors[0].ID = "changed"
	first[0].Permissions.Operations[0] = "changed.operation"

	again := Builtins()
	if again[0].Requires.Connectors[0].ID != "aws" {
		t.Fatalf("mutating Builtins result changed registry: %+v", again[0].Requires.Connectors)
	}
	if again[0].Permissions.Operations[0] != "aws.credentials.read" {
		t.Fatalf("mutating Builtins permissions changed registry: %+v", again[0].Permissions.Operations)
	}
}

func TestDecodeManifestRejectsMissingAndUnknownFields(t *testing.T) {
	base := validManifestFields("one")
	delete(base, "name")
	_, err := decodeManifest(marshalManifestFields(t, base))
	if err == nil || !strings.Contains(err.Error(), `missing required field "name"`) {
		t.Fatalf("missing name error = %v", err)
	}

	base = validManifestFields("one")
	base["surprise"] = true
	_, err = decodeManifest(marshalManifestFields(t, base))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestValidateBuiltinManifestRejectsInvalidEngineAndRuntime(t *testing.T) {
	manifest, err := decodeManifest(marshalManifestFields(t, validManifestFields("one")))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	badEngine := manifest
	badEngine.Engines.Kervik = "latest"
	if err := validateBuiltinManifest(badEngine); err == nil || !strings.Contains(err.Error(), "engines.kervik") {
		t.Fatalf("invalid engine error = %v", err)
	}

	badRuntime := manifest
	badRuntime.Runtime.Type = "native"
	if err := validateBuiltinManifest(badRuntime); err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("invalid runtime error = %v", err)
	}

	badEntrypoint := manifest
	badEntrypoint.Runtime.Entrypoint = "one"
	if err := validateBuiltinManifest(badEntrypoint); err == nil || !strings.Contains(err.Error(), "host:<entrypoint>") {
		t.Fatalf("invalid entrypoint error = %v", err)
	}
}

func TestLoadBuiltinsRejectsDuplicateIDs(t *testing.T) {
	data := marshalManifestFields(t, validManifestFields("duplicate"))
	manifestFS := fstest.MapFS{
		"addons/a/addon.json": &fstest.MapFile{Data: data},
		"addons/b/addon.json": &fstest.MapFile{Data: data},
	}
	_, err := loadBuiltins(manifestFS)
	if err == nil || !strings.Contains(err.Error(), `duplicate add-on id "duplicate"`) {
		t.Fatalf("duplicate id error = %v", err)
	}
}

func validManifestFields(id string) map[string]any {
	return map[string]any{
		"$schema":       "../addon.schema.json",
		"schemaVersion": 1,
		"id":            id,
		"version":       "2.0.0",
		"publisher":     "ezcloud",
		"engines":       map[string]any{"kervik": ">=2.0.0 <3.0.0", "ezcloud": ">=2.0.0 <3.0.0"},
		"kind":          "addon",
		"runtime":       map[string]any{"type": "builtin", "entrypoint": "host:fixture"},
		"requires":      map[string]any{"connectors": []map[string]any{}},
		"name":          "Fixture",
		"description":   "Fixture add-on.",
		"icon":          "shippingbox",
		"category":      "DevOps",
		"permissions":   map[string]any{"operations": []string{}},
		"contributes":   map[string]any{},
	}
}

func marshalManifestFields(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest fixture: %v", err)
	}
	return data
}

func TestDescriptorProjection(t *testing.T) {
	for _, m := range Builtins() {
		d := m.Descriptor()
		clouds := make([]string, len(m.Requires.Connectors))
		for i, requirement := range m.Requires.Connectors {
			clouds[i] = requirement.ID
		}
		want := Descriptor{ID: m.ID, Name: m.Name, Description: m.Description, Icon: m.Icon, Clouds: clouds, Category: m.Category, Kind: m.Kind}
		if !reflect.DeepEqual(d, want) {
			t.Errorf("Descriptor() = %+v, want %+v", d, want)
		}
	}
	ds := Descriptors()
	if len(ds) != len(Builtins()) {
		t.Fatalf("Descriptors() length = %d, want %d", len(ds), len(Builtins()))
	}
	if ds[2].Clouds == nil || len(ds[2].Clouds) != 0 {
		t.Fatalf("connector-free descriptor clouds = %#v, want non-nil empty compatibility array", ds[2].Clouds)
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

func TestManifestCurrentContractRoundTrip(t *testing.T) {
	const currentExample = `{
  "$schema": "../addon.schema.json",
  "schemaVersion": 1,
  "id": "ec2-launch-templates",
  "version": "2.0.0",
  "publisher": "ezcloud",
  "engines": { "kervik": ">=2.0.0 <3.0.0", "ezcloud": ">=2.0.0 <3.0.0" },
  "kind": "addon",
  "runtime": { "type": "builtin", "entrypoint": "host:ec2-launch-templates" },
  "requires": { "connectors": [{ "id": "aws", "api": ">=1 <2" }] },
  "name": "Launch Templates",
  "description": "Edit EC2 launch templates like plain configs.",
  "icon": "server.rack",
  "category": "DevOps",
  "permissions": {
    "operations": ["aws.ec2.launchTemplates.read", "aws.ec2.launchTemplates.write"]
  },
  "contributes": {}
}`
	m, err := decodeManifest([]byte(currentExample))
	if err != nil {
		t.Fatalf("decode current example: %v", err)
	}
	if err := validateBuiltinManifest(m); err != nil {
		t.Fatalf("validate current example: %v", err)
	}
	if m.ID != "ec2-launch-templates" || m.Runtime.Type != "builtin" || m.Category != CategoryDevOps {
		t.Fatalf("unexpected decode: %+v", m)
	}
	if len(m.Requires.Connectors) != 1 || m.Requires.Connectors[0].ID != "aws" || m.Requires.Connectors[0].API != ">=1 <2" {
		t.Fatalf("requires.connectors not decoded: %+v", m.Requires.Connectors)
	}
	if len(m.Permissions.Operations) != 2 {
		t.Fatalf("permissions.operations not decoded: %+v", m.Permissions.Operations)
	}

	// Round-trip through JSON must be stable so the typed contract cannot
	// silently drift from its manifest representation.
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
