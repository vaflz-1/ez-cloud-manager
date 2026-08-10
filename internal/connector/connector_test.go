package connector

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuiltinsAreValidatedAndStable(t *testing.T) {
	all := Builtins()
	wantIDs := []string{"aws", "azure", "gcp"}
	if len(all) != len(wantIDs) {
		t.Fatalf("Builtins() returned %d manifests, want %d", len(all), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		manifest := all[i]
		if manifest.ID != wantID {
			t.Errorf("Builtins()[%d].ID = %q, want stable ID order entry %q", i, manifest.ID, wantID)
		}
		if manifest.Runtime.Type != "builtin" || manifest.Runtime.Entrypoint != "host:"+manifest.ID {
			t.Errorf("%s has non-compiled runtime metadata: %+v", manifest.ID, manifest.Runtime)
		}
		if manifest.Name == "" || manifest.Description == "" || manifest.Icon == "" {
			t.Errorf("%s is missing display metadata: %+v", manifest.ID, manifest)
		}
		if len(manifest.Provides.Operations) == 0 {
			t.Errorf("%s provides no typed operations", manifest.ID)
		}
	}
}

func TestBuiltinsReturnsIndependentCopies(t *testing.T) {
	first := Builtins()
	first[0].Provides.Operations[0] = "changed.operation"

	again := Builtins()
	if again[0].Provides.Operations[0] == "changed.operation" {
		t.Fatal("mutating Builtins result changed the registry")
	}
}

func TestByID(t *testing.T) {
	manifest, ok := ByID("aws")
	if !ok || manifest.Name != "Amazon Web Services" {
		t.Fatalf("ByID(aws) = %+v, %v", manifest, ok)
	}
	if _, ok := ByID("missing"); ok {
		t.Fatal("ByID should return false for an unknown connector")
	}
}

func TestDecodeManifestIsStrict(t *testing.T) {
	fields := validManifestFields("fixture")
	delete(fields, "apiVersion")
	_, err := decodeManifest(marshalManifestFields(t, fields))
	if err == nil || !strings.Contains(err.Error(), `missing required field "apiVersion"`) {
		t.Fatalf("missing apiVersion error = %v", err)
	}

	fields = validManifestFields("fixture")
	fields["surprise"] = true
	_, err = decodeManifest(marshalManifestFields(t, fields))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	data := append(marshalManifestFields(t, validManifestFields("fixture")), []byte(" {}")...)
	_, err = decodeManifest(data)
	if err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}

func TestValidateBuiltinManifestRejectsContractViolations(t *testing.T) {
	base := decodeValidManifest(t, "fixture")
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		message string
	}{
		{"schema version", func(m *Manifest) { m.SchemaVersion = 2 }, "schemaVersion"},
		{"id", func(m *Manifest) { m.ID = "Bad ID" }, "invalid id"},
		{"version", func(m *Manifest) { m.Version = "latest" }, "invalid version"},
		{"api version", func(m *Manifest) { m.APIVersion = "v1" }, "invalid apiVersion"},
		{"kind", func(m *Manifest) { m.Kind = "database" }, "unsupported kind"},
		{"engine", func(m *Manifest) { m.Engines.Kervik = "latest" }, "engines.kervik"},
		{"runtime type", func(m *Manifest) { m.Runtime.Type = "native" }, "runtime.type"},
		{"runtime entrypoint", func(m *Manifest) { m.Runtime.Entrypoint = "host:other" }, "runtime.entrypoint"},
		{"empty operations", func(m *Manifest) { m.Provides.Operations = []string{} }, "at least one operation"},
		{"invalid operation", func(m *Manifest) { m.Provides.Operations = []string{"shell"} }, "invalid operation"},
		{"foreign operation", func(m *Manifest) { m.Provides.Operations = []string{"other.credentials.read"} }, "outside connector namespace"},
		{"duplicate operation", func(m *Manifest) {
			m.Provides.Operations = []string{"fixture.credentials.read", "fixture.credentials.read"}
		}, "duplicate operation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := base
			manifest.Provides.Operations = append([]string(nil), base.Provides.Operations...)
			test.mutate(&manifest)
			err := validateBuiltinManifest(manifest)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestLoadBuiltinsRejectsDuplicatesAndSortsByID(t *testing.T) {
	manifestFS := fstest.MapFS{
		"connectors/z/connector.json": &fstest.MapFile{Data: marshalManifestFields(t, validManifestFields("zeta"))},
		"connectors/a/connector.json": &fstest.MapFile{Data: marshalManifestFields(t, validManifestFields("alpha"))},
	}
	all, err := loadBuiltins(manifestFS)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{all[0].ID, all[1].ID}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stable order = %v, want %v", got, want)
	}

	duplicate := marshalManifestFields(t, validManifestFields("same"))
	_, err = loadBuiltins(fstest.MapFS{
		"connectors/a/connector.json": &fstest.MapFile{Data: duplicate},
		"connectors/b/connector.json": &fstest.MapFile{Data: duplicate},
	})
	if err == nil || !strings.Contains(err.Error(), `duplicate connector id "same"`) {
		t.Fatalf("duplicate id error = %v", err)
	}
}

func decodeValidManifest(t *testing.T, id string) Manifest {
	t.Helper()
	manifest, err := decodeManifest(marshalManifestFields(t, validManifestFields(id)))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func validManifestFields(id string) map[string]any {
	return map[string]any{
		"$schema":       "../connector.schema.json",
		"schemaVersion": 1,
		"id":            id,
		"version":       "2.0.0",
		"apiVersion":    "1.0.0",
		"kind":          "cloud",
		"publisher":     "ezcloud",
		"engines":       map[string]any{"kervik": ">=2.0.0 <3.0.0", "ezcloud": ">=2.0.0 <3.0.0"},
		"runtime":       map[string]any{"type": "builtin", "entrypoint": "host:" + id},
		"name":          "Fixture",
		"description":   "Fixture connector.",
		"icon":          "cloud.fill",
		"provides":      map[string]any{"operations": []string{id + ".credentials.read"}},
	}
}

func marshalManifestFields(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
