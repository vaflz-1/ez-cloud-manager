// Package plugin defines the add-on manifest shape and first-party registry.
// First-party metadata is loaded from the physical addons/ package tree and
// embedded in the application binary. Implementations remain compiled into
// the Go/Swift host until the permissioned broker phase, which is why these
// manifests truthfully use the "builtin" runtime.
package plugin

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

// Category groups add-ons for marketplace/catalog browsing (docs/PLATFORM.md
// "Marketplace": DevOps/DevSecOps/AIOps/FinOps).
type Category string

const (
	CategoryDevOps    Category = "DevOps"
	CategoryDevSecOps Category = "DevSecOps"
	CategoryAIOps     Category = "AIOps"
	CategoryFinOps    Category = "FinOps"
)

// Built-in plugin IDs — the single source of truth every other package
// (profile migration, the CLI, tests) references instead of duplicating
// string literals.
const (
	CloudAccountsID   = "cloud-accounts"
	LaunchTemplatesID = "ec2-launch-templates"
	TransferID        = "transfer"
)

// Engines is an add-on's compatibility range against the host app.
type Engines struct {
	Kervik  string `json:"kervik"`
	Ezcloud string `json:"ezcloud,omitempty"`
}

// Runtime describes how the host reaches an add-on implementation. All
// current first-party packages use host entrypoints because their code is
// compiled into the app.
type Runtime struct {
	Type       string `json:"type"`
	Entrypoint string `json:"entrypoint"`
}

// Requirements declares typed platform dependencies without exposing
// credential files, environment variables or vendor CLI invocation details.
type Requirements struct {
	Connectors []ConnectorRequirement `json:"connectors"`
}

// ConnectorRequirement pins an add-on to a compatible major connector API.
// The package ID and API range are independent from the connector package
// version and the host engine version.
type ConnectorRequirement struct {
	ID  string `json:"id"`
	API string `json:"api"`
}

// Permissions is the broker-facing operation consent surface. Operation IDs
// are stable API capabilities, not shell command prefixes.
type Permissions struct {
	Operations []string `json:"operations"`
}

// Kind distinguishes normal add-ons from platform-owned system surfaces.
// Connections keeps its legacy package ID during the current transition, but
// it is explicitly system-owned and is not the target removable-add-on model.
type Kind string

const (
	KindAddon  Kind = "addon"
	KindSystem Kind = "system"
)

type MenuContribution struct {
	Menu    string `json:"menu"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

type SidebarContribution struct {
	Section string `json:"section"`
	ID      string `json:"id"`
	View    string `json:"view"`
}

type ViewContribution struct {
	ID   string `json:"id"`
	Spec string `json:"spec"`
}

type SettingsContribution struct {
	Pane   string `json:"pane"`
	Schema string `json:"schema"`
}

type CommandContribution struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Exec  string `json:"exec"`
}

type ResourceContribution struct {
	Type  string   `json:"type"`
	List  string   `json:"list"`
	Bind  string   `json:"bind"`
	Verbs []string `json:"verbs"`
}

// Contributes mirrors docs/PLATFORM.md's contribution points 1:1 — none of
// these are read by anything until the declarative renderer exists (P2/P3);
// P1 built-ins leave every field empty.
type Contributes struct {
	Menus     []MenuContribution     `json:"menus,omitempty"`
	Sidebar   []SidebarContribution  `json:"sidebar,omitempty"`
	Views     []ViewContribution     `json:"views,omitempty"`
	Settings  []SettingsContribution `json:"settings,omitempty"`
	Commands  []CommandContribution  `json:"commands,omitempty"`
	Resources []ResourceContribution `json:"resources,omitempty"`
}

// Manifest is the versioned addons/*/addon.json contract. Connector
// dependencies and permission grants are typed IDs so the future broker can
// authorize operations without exposing raw commands or environment access.
type Manifest struct {
	Schema        string       `json:"$schema,omitempty"`
	SchemaVersion int          `json:"schemaVersion"`
	ID            string       `json:"id"`
	Version       string       `json:"version"`
	Publisher     string       `json:"publisher"`
	Engines       Engines      `json:"engines"`
	Kind          Kind         `json:"kind"`
	Runtime       Runtime      `json:"runtime"`
	Requires      Requirements `json:"requires"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	Icon          string       `json:"icon"` // SF Symbol name
	Category      Category     `json:"category"`
	Permissions   Permissions  `json:"permissions"`
	Contributes   Contributes  `json:"contributes"`
}

var (
	manifestIDPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	manifestVersionPattern   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	engineRangePattern       = regexp.MustCompile(`^>=[0-9]+\.[0-9]+\.[0-9]+ <[0-9]+\.[0-9]+\.[0-9]+$`)
	connectorAPIRangePattern = regexp.MustCompile(`^>=[0-9]+ <[0-9]+$`)
	operationIDPattern       = regexp.MustCompile(`^[a-z][A-Za-z0-9-]*(?:\.[a-z][A-Za-z0-9-]*)+$`)
)

var requiredManifestFields = []string{
	"schemaVersion",
	"id",
	"version",
	"publisher",
	"engines",
	"kind",
	"runtime",
	"requires",
	"name",
	"description",
	"icon",
	"category",
	"permissions",
	"contributes",
}

var builtinManifests = mustLoadBuiltins()

// Descriptor is the trimmed view a hub/catalog actually renders — no
// Contributes/Permissions, which the P1 hub never inspects.
type Descriptor struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Clouds      []string `json:"clouds"`
	Category    Category `json:"category"`
	Kind        Kind     `json:"kind"`
}

// Descriptor projects m down to what a catalog/hub listing needs.
func (m Manifest) Descriptor() Descriptor {
	connectors := make([]string, len(m.Requires.Connectors))
	for i, requirement := range m.Requires.Connectors {
		connectors[i] = requirement.ID
	}
	return Descriptor{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Icon:        m.Icon,
		// clouds is retained only as a compatibility projection for the
		// current Swift descriptor decoder. The package contract is
		// requires.connectors.
		Clouds:   connectors,
		Category: m.Category,
		Kind:     m.Kind,
	}
}

// Builtins is the first-party registry loaded from embedded addons/*/addon.json
// manifests. It does not scan mutable user directories or execute package
// code. A fresh copy prevents callers from mutating the process-wide registry.
func Builtins() []Manifest {
	out := make([]Manifest, len(builtinManifests))
	for i, manifest := range builtinManifests {
		out[i] = cloneManifest(manifest)
	}
	return out
}

func mustLoadBuiltins() []Manifest {
	manifests, err := loadBuiltins(packageassets.Embedded())
	if err != nil {
		panic(fmt.Sprintf("load embedded add-on manifests: %v", err))
	}
	return manifests
}

func loadBuiltins(manifestFS fs.FS) ([]Manifest, error) {
	paths, err := fs.Glob(manifestFS, "addons/*/addon.json")
	if err != nil {
		return nil, fmt.Errorf("discover manifests: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no first-party add-on manifests found")
	}
	sort.Strings(paths)

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
			return nil, fmt.Errorf("duplicate add-on id %q in %s and %s", manifest.ID, previous, path)
		}
		seen[manifest.ID] = path
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func decodeManifest(data []byte) (Manifest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Manifest{}, err
	}
	for _, field := range requiredManifestFields {
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
	if !manifestIDPattern.MatchString(manifest.ID) {
		return fmt.Errorf("invalid id %q", manifest.ID)
	}
	if !manifestVersionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid version %q", manifest.Version)
	}
	if strings.TrimSpace(manifest.Publisher) == "" {
		return fmt.Errorf("publisher is required")
	}
	if !engineRangePattern.MatchString(manifest.Engines.Kervik) {
		return fmt.Errorf("invalid engines.kervik range %q", manifest.Engines.Kervik)
	}
	if manifest.Engines.Ezcloud != "" && !engineRangePattern.MatchString(manifest.Engines.Ezcloud) {
		return fmt.Errorf("invalid legacy engines.ezcloud range %q", manifest.Engines.Ezcloud)
	}
	if manifest.Requires.Connectors == nil {
		return fmt.Errorf("requires.connectors is required; use [] when no connector is needed")
	}
	if manifest.Runtime.Type != "builtin" {
		return fmt.Errorf("first-party compiled add-on runtime.type = %q, want %q", manifest.Runtime.Type, "builtin")
	}
	if !strings.HasPrefix(manifest.Runtime.Entrypoint, "host:") {
		return fmt.Errorf("builtin runtime.entrypoint %q must use host:<entrypoint>", manifest.Runtime.Entrypoint)
	}
	entrypoint := strings.TrimPrefix(manifest.Runtime.Entrypoint, "host:")
	if !manifestIDPattern.MatchString(entrypoint) {
		return fmt.Errorf("builtin runtime.entrypoint %q must use host:<entrypoint>", manifest.Runtime.Entrypoint)
	}
	switch manifest.Kind {
	case KindAddon, KindSystem:
	default:
		return fmt.Errorf("unsupported package kind %q", manifest.Kind)
	}
	if manifest.ID == CloudAccountsID && manifest.Kind != KindSystem {
		return fmt.Errorf("connections package must be kind %q during the platform transition", KindSystem)
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Description) == "" || strings.TrimSpace(manifest.Icon) == "" {
		return fmt.Errorf("name, description and icon are required")
	}
	switch manifest.Category {
	case CategoryDevOps, CategoryDevSecOps, CategoryAIOps, CategoryFinOps:
	default:
		return fmt.Errorf("unsupported category %q", manifest.Category)
	}
	seenConnectors := make(map[string]struct{}, len(manifest.Requires.Connectors))
	for _, connector := range manifest.Requires.Connectors {
		if !manifestIDPattern.MatchString(connector.ID) {
			return fmt.Errorf("invalid connector id %q", connector.ID)
		}
		if !connectorAPIRangePattern.MatchString(connector.API) {
			return fmt.Errorf("invalid connector %q api range %q", connector.ID, connector.API)
		}
		if _, duplicate := seenConnectors[connector.ID]; duplicate {
			return fmt.Errorf("duplicate connector id %q", connector.ID)
		}
		seenConnectors[connector.ID] = struct{}{}
	}
	if manifest.Permissions.Operations == nil {
		return fmt.Errorf("permissions.operations is required; use [] when no broker operation is needed")
	}
	seenOperations := make(map[string]struct{}, len(manifest.Permissions.Operations))
	for _, operation := range manifest.Permissions.Operations {
		if !operationIDPattern.MatchString(operation) {
			return fmt.Errorf("invalid operation id %q", operation)
		}
		if _, duplicate := seenOperations[operation]; duplicate {
			return fmt.Errorf("duplicate operation id %q", operation)
		}
		seenOperations[operation] = struct{}{}
	}
	return nil
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Requires.Connectors = cloneSlice(manifest.Requires.Connectors)
	manifest.Permissions.Operations = cloneSlice(manifest.Permissions.Operations)
	manifest.Contributes.Menus = cloneSlice(manifest.Contributes.Menus)
	manifest.Contributes.Sidebar = cloneSlice(manifest.Contributes.Sidebar)
	manifest.Contributes.Views = cloneSlice(manifest.Contributes.Views)
	manifest.Contributes.Settings = cloneSlice(manifest.Contributes.Settings)
	manifest.Contributes.Commands = cloneSlice(manifest.Contributes.Commands)
	manifest.Contributes.Resources = cloneSlice(manifest.Contributes.Resources)
	for i := range manifest.Contributes.Resources {
		manifest.Contributes.Resources[i].Verbs = cloneSlice(manifest.Contributes.Resources[i].Verbs)
	}
	return manifest
}

func cloneSlice[T any](source []T) []T {
	if source == nil {
		return nil
	}
	return append(make([]T, 0, len(source)), source...)
}

// Descriptors is Builtins() projected through Descriptor, in registry order.
func Descriptors() []Descriptor {
	all := Builtins()
	out := make([]Descriptor, len(all))
	for i, m := range all {
		out[i] = m.Descriptor()
	}
	return out
}

// ByID looks up one built-in by id.
func ByID(id string) (Manifest, bool) {
	for _, m := range Builtins() {
		if m.ID == id {
			return m, true
		}
	}
	return Manifest{}, false
}
