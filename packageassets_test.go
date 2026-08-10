package packageassets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type connectorManifest struct {
	Schema        string            `json:"$schema"`
	SchemaVersion int               `json:"schemaVersion"`
	ID            string            `json:"id"`
	Version       string            `json:"version"`
	APIVersion    string            `json:"apiVersion"`
	Kind          string            `json:"kind"`
	Publisher     string            `json:"publisher"`
	Engines       engines           `json:"engines"`
	Runtime       connectorRuntime  `json:"runtime"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Icon          string            `json:"icon"`
	Provides      connectorProvides `json:"provides"`
}

type engines struct {
	Kervik  string `json:"kervik"`
	Ezcloud string `json:"ezcloud"`
}

type connectorRuntime struct {
	Type       string `json:"type"`
	Entrypoint string `json:"entrypoint"`
}

type connectorProvides struct {
	Operations []string `json:"operations"`
}

var (
	connectorIDPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	connectorVersionPattern   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	connectorEnginePattern    = regexp.MustCompile(`^>=[0-9]+\.[0-9]+\.[0-9]+ <[0-9]+\.[0-9]+\.[0-9]+$`)
	connectorOperationPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9-]*(?:\.[a-z][A-Za-z0-9-]*)+$`)
)

func TestEmbeddedConnectorManifests(t *testing.T) {
	paths, err := fs.Glob(Embedded(), "connectors/*/connector.json")
	if err != nil {
		t.Fatalf("discover connector manifests: %v", err)
	}
	sort.Strings(paths)
	if len(paths) != 3 {
		t.Fatalf("connector manifests = %v, want aws/gcp/azure", paths)
	}

	wantIDs := map[string]bool{"aws": false, "gcp": false, "azure": false}
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := fs.ReadFile(Embedded(), path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		manifest, err := decodeConnectorManifest(data)
		if err != nil {
			t.Errorf("decode %s: %v", path, err)
			continue
		}
		if err := validateConnectorManifest(manifest); err != nil {
			t.Errorf("validate %s: %v", path, err)
		}
		if previous, duplicate := seen[manifest.ID]; duplicate {
			t.Errorf("duplicate connector id %q in %s and %s", manifest.ID, previous, path)
		}
		seen[manifest.ID] = path
		if _, expected := wantIDs[manifest.ID]; !expected {
			t.Errorf("unexpected connector id %q", manifest.ID)
		} else {
			wantIDs[manifest.ID] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("missing connector %q", id)
		}
	}
}

func TestEmbeddedManifestSchemasAreJSON(t *testing.T) {
	for _, path := range []string{"addons/addon.schema.json", "connectors/connector.schema.json"} {
		data, err := fs.ReadFile(Embedded(), path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["type"] != "object" {
			t.Errorf("%s is not a draft 2020-12 object schema", path)
		}
	}
}

func decodeConnectorManifest(data []byte) (connectorManifest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return connectorManifest{}, err
	}
	for _, field := range []string{"schemaVersion", "id", "version", "apiVersion", "kind", "publisher", "engines", "runtime", "name", "description", "icon", "provides"} {
		if _, ok := fields[field]; !ok {
			return connectorManifest{}, fmt.Errorf("missing required field %q", field)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest connectorManifest
	if err := decoder.Decode(&manifest); err != nil {
		return connectorManifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return connectorManifest{}, fmt.Errorf("multiple JSON values")
		}
		return connectorManifest{}, err
	}
	return manifest, nil
}

func validateConnectorManifest(manifest connectorManifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion = %d, want 1", manifest.SchemaVersion)
	}
	if !connectorIDPattern.MatchString(manifest.ID) {
		return fmt.Errorf("invalid id %q", manifest.ID)
	}
	if !connectorVersionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid version %q", manifest.Version)
	}
	if !connectorVersionPattern.MatchString(manifest.APIVersion) {
		return fmt.Errorf("invalid apiVersion %q", manifest.APIVersion)
	}
	if manifest.Kind != "cloud" {
		return fmt.Errorf("kind = %q, want cloud for bundled defaults", manifest.Kind)
	}
	if strings.TrimSpace(manifest.Publisher) == "" {
		return fmt.Errorf("publisher is required")
	}
	if !connectorEnginePattern.MatchString(manifest.Engines.Kervik) {
		return fmt.Errorf("invalid engines.kervik range %q", manifest.Engines.Kervik)
	}
	if manifest.Engines.Ezcloud != "" && !connectorEnginePattern.MatchString(manifest.Engines.Ezcloud) {
		return fmt.Errorf("invalid legacy engines.ezcloud range %q", manifest.Engines.Ezcloud)
	}
	if manifest.Runtime.Type != "builtin" {
		return fmt.Errorf("runtime.type = %q, want builtin", manifest.Runtime.Type)
	}
	if manifest.Runtime.Entrypoint != "host:"+manifest.ID {
		return fmt.Errorf("runtime.entrypoint = %q, want host:%s", manifest.Runtime.Entrypoint, manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Description) == "" || strings.TrimSpace(manifest.Icon) == "" {
		return fmt.Errorf("name, description and icon are required")
	}
	if len(manifest.Provides.Operations) == 0 {
		return fmt.Errorf("at least one provided operation is required")
	}
	seen := make(map[string]struct{}, len(manifest.Provides.Operations))
	for _, operation := range manifest.Provides.Operations {
		if !connectorOperationPattern.MatchString(operation) {
			return fmt.Errorf("invalid operation %q", operation)
		}
		if !strings.HasPrefix(operation, manifest.ID+".") {
			return fmt.Errorf("operation %q is outside connector namespace %q", operation, manifest.ID)
		}
		if _, duplicate := seen[operation]; duplicate {
			return fmt.Errorf("duplicate operation %q", operation)
		}
		seen[operation] = struct{}{}
	}
	return nil
}
