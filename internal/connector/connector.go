// Package connector defines the metadata contract and registry for trusted,
// first-party connection backends.
//
// Manifests are read from the connector package tree embedded in the binary.
// This package does not scan mutable directories or load executable code;
// every current connector implementation remains compiled into the host.
package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	packageassets "ez-cloud-manager"
)

// Kind identifies the environment a connector reaches.
type Kind string

const (
	KindCloud      Kind = "cloud"
	KindDevice     Kind = "device"
	KindSelfHosted Kind = "self-hosted"
)

// Engines is the connector's compatibility range against the host app.
type Engines struct {
	Kervik  string `json:"kervik"`
	Ezcloud string `json:"ezcloud,omitempty"`
}

// Runtime records how the compiled host reaches the connector. Embedded
// first-party connectors must use host:<id>; this is metadata, not a dynamic
// module loader.
type Runtime struct {
	Type       string `json:"type"`
	Entrypoint string `json:"entrypoint"`
}

// Provides is the typed operation surface that add-ons may eventually call
// through a permission broker. Operation IDs are capabilities, not commands.
type Provides struct {
	Operations []string `json:"operations"`
}

// Manifest is the versioned connectors/*/connector.json contract.
type Manifest struct {
	Schema        string   `json:"$schema,omitempty"`
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	APIVersion    string   `json:"apiVersion"`
	Kind          Kind     `json:"kind"`
	Publisher     string   `json:"publisher"`
	Engines       Engines  `json:"engines"`
	Runtime       Runtime  `json:"runtime"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Icon          string   `json:"icon"`
	Provides      Provides `json:"provides"`
}

var (
	idPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	versionPattern   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	enginePattern    = regexp.MustCompile(`^>=[0-9]+\.[0-9]+\.[0-9]+ <[0-9]+\.[0-9]+\.[0-9]+$`)
	operationPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9-]*(?:\.[a-z][A-Za-z0-9-]*)+$`)
)

var requiredFields = []string{
	"schemaVersion",
	"id",
	"version",
	"apiVersion",
	"kind",
	"publisher",
	"engines",
	"runtime",
	"name",
	"description",
	"icon",
	"provides",
}

var builtinManifests = mustLoadBuiltins()

// Builtins returns the validated first-party connector manifests in stable ID
// order. A deep copy prevents callers from mutating the process registry.
func Builtins() []Manifest {
	out := make([]Manifest, len(builtinManifests))
	for i, manifest := range builtinManifests {
		out[i] = cloneManifest(manifest)
	}
	return out
}

// ByID looks up one compiled first-party connector manifest.
func ByID(id string) (Manifest, bool) {
	index := sort.Search(len(builtinManifests), func(i int) bool {
		return builtinManifests[i].ID >= id
	})
	if index == len(builtinManifests) || builtinManifests[index].ID != id {
		return Manifest{}, false
	}
	return cloneManifest(builtinManifests[index]), true
}

func mustLoadBuiltins() []Manifest {
	manifests, err := loadBuiltins(packageassets.Embedded())
	if err != nil {
		panic(fmt.Sprintf("load embedded connector manifests: %v", err))
	}
	return manifests
}

func loadBuiltins(manifestFS fs.FS) ([]Manifest, error) {
	paths, err := fs.Glob(manifestFS, "connectors/*/connector.json")
	if err != nil {
		return nil, fmt.Errorf("discover manifests: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no first-party connector manifests found")
	}

	manifests := make([]Manifest, 0, len(paths))
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := fs.ReadFile(manifestFS, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		manifest, err := decodeManifest(data)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		if err := validateBuiltinManifest(manifest); err != nil {
			return nil, fmt.Errorf("validate %s: %w", path, err)
		}
		if previous, duplicate := seen[manifest.ID]; duplicate {
			return nil, fmt.Errorf("duplicate connector id %q in %s and %s", manifest.ID, previous, path)
		}
		seen[manifest.ID] = path
		manifests = append(manifests, manifest)
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].ID < manifests[j].ID
	})
	return manifests, nil
}

func decodeManifest(data []byte) (Manifest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Manifest{}, err
	}
	for _, field := range requiredFields {
		if _, ok := fields[field]; !ok {
			return Manifest{}, fmt.Errorf("missing required field %q", field)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("multiple JSON values")
		}
		return Manifest{}, err
	}
	return manifest, nil
}

func validateBuiltinManifest(manifest Manifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion = %d, want 1", manifest.SchemaVersion)
	}
	if !idPattern.MatchString(manifest.ID) {
		return fmt.Errorf("invalid id %q", manifest.ID)
	}
	if !versionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid version %q", manifest.Version)
	}
	if !versionPattern.MatchString(manifest.APIVersion) {
		return fmt.Errorf("invalid apiVersion %q", manifest.APIVersion)
	}
	switch manifest.Kind {
	case KindCloud, KindDevice, KindSelfHosted:
	default:
		return fmt.Errorf("unsupported kind %q", manifest.Kind)
	}
	if strings.TrimSpace(manifest.Publisher) == "" {
		return fmt.Errorf("publisher is required")
	}
	if !enginePattern.MatchString(manifest.Engines.Kervik) {
		return fmt.Errorf("invalid engines.kervik range %q", manifest.Engines.Kervik)
	}
	if manifest.Engines.Ezcloud != "" && !enginePattern.MatchString(manifest.Engines.Ezcloud) {
		return fmt.Errorf("invalid legacy engines.ezcloud range %q", manifest.Engines.Ezcloud)
	}
	if manifest.Runtime.Type != "builtin" {
		return fmt.Errorf("first-party compiled connector runtime.type = %q, want %q", manifest.Runtime.Type, "builtin")
	}
	if manifest.Runtime.Entrypoint != "host:"+manifest.ID {
		return fmt.Errorf("builtin runtime.entrypoint = %q, want host:%s", manifest.Runtime.Entrypoint, manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Description) == "" || strings.TrimSpace(manifest.Icon) == "" {
		return fmt.Errorf("name, description and icon are required")
	}
	if manifest.Provides.Operations == nil || len(manifest.Provides.Operations) == 0 {
		return fmt.Errorf("provides.operations must contain at least one operation")
	}
	seenOperations := make(map[string]struct{}, len(manifest.Provides.Operations))
	for _, operation := range manifest.Provides.Operations {
		if !operationPattern.MatchString(operation) {
			return fmt.Errorf("invalid operation id %q", operation)
		}
		if !strings.HasPrefix(operation, manifest.ID+".") {
			return fmt.Errorf("operation %q is outside connector namespace %q", operation, manifest.ID)
		}
		if _, duplicate := seenOperations[operation]; duplicate {
			return fmt.Errorf("duplicate operation id %q", operation)
		}
		seenOperations[operation] = struct{}{}
	}
	return nil
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Provides.Operations = append([]string(nil), manifest.Provides.Operations...)
	return manifest
}
