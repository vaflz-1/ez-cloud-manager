// Package plugin defines the plugin manifest shape and the fixed P1 registry
// of built-in plugins (docs/PLATFORM.md phase P1: "Plugin host"). It has no
// I/O and no dependents of its own — internal/profile and cmd/ezcloud depend
// on it, never the other way around.
//
// P1 ships exactly three built-ins, compiled into the Go/Swift binaries
// rather than loaded from a manifest file on disk — hence the "builtin"
// Runtime value, a third tier alongside docs/PLATFORM.md's documented
// "declarative"/native-JSON-RPC ones. Manifest's shape is nonetheless fixed
// to the doc's "Manifest & contribution points" example now (plus
// Name/Description/Icon/Category, needed for any hub/catalog listing but not
// shown in that example) so a future external Tier-1 plugin.json needs zero
// struct changes to load — Contributes/Permissions simply stay zero-valued
// until the declarative renderer + credential broker exist (P2/P3).
package plugin

// Category groups plugins for marketplace/catalog browsing (docs/PLATFORM.md
// "Marketplace": DevOps/DevSecOps/AIOps/FinOps).
type Category string

const (
	CategoryDevOps    Category = "DevOps"
	CategoryDevSecOps Category = "DevSecOps"
	CategoryAIOps     Category = "AIOps"
)

// Built-in plugin IDs — the single source of truth every other package
// (profile migration, the CLI, tests) references instead of duplicating
// string literals.
const (
	CloudAccountsID   = "cloud-accounts"
	LaunchTemplatesID = "ec2-launch-templates"
	TransferID        = "transfer"
)

// Engines is a plugin's compatibility range against the host app.
type Engines struct {
	Ezcloud string `json:"ezcloud"`
}

// Permissions is the install-time consent surface docs/PLATFORM.md's
// credential broker enforces against — unused by any P1 built-in (they run
// with full host trust, not through the broker), but part of the fixed shape.
type Permissions struct {
	CLI     []string `json:"cli,omitempty"`
	Env     []string `json:"env,omitempty"`
	Network string   `json:"network,omitempty"`
}

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

// Manifest is the plugin.json shape from docs/PLATFORM.md's "Manifest &
// contribution points" example, plus Name/Description/Icon/Category — an
// addition beyond that example, required for any hub/catalog listing
// (VS-Code-package.json-style), disclosed here rather than silently added.
type Manifest struct {
	ID          string      `json:"id"`
	Version     string      `json:"version"`
	Publisher   string      `json:"publisher"`
	Engines     Engines     `json:"engines"`
	Clouds      []string    `json:"clouds"`
	Runtime     string      `json:"runtime"` // "declarative" | "native" | "builtin" (P1 only uses "builtin")
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Icon        string      `json:"icon"` // SF Symbol name
	Category    Category    `json:"category"`
	Permissions Permissions `json:"permissions"`
	Contributes Contributes `json:"contributes"`
}

// Descriptor is the trimmed view a hub/catalog actually renders — no
// Contributes/Permissions, which the P1 hub never inspects.
type Descriptor struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Clouds      []string `json:"clouds"`
	Category    Category `json:"category"`
}

// Descriptor projects m down to what a catalog/hub listing needs.
func (m Manifest) Descriptor() Descriptor {
	return Descriptor{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Icon:        m.Icon,
		Clouds:      m.Clouds,
		Category:    m.Category,
	}
}

// Builtins is the entire P1 "registry" — no filesystem scanning, no
// marketplace yet (P2 replaces/extends this with installed-plugin discovery
// under .../EZCloudManager/plugins/).
func Builtins() []Manifest {
	return []Manifest{
		{
			ID: CloudAccountsID, Version: "2.0.0", Publisher: "ezcloud",
			Engines: Engines{Ezcloud: ">=2.0"}, Clouds: []string{"aws", "gcp", "azure"},
			Runtime: "builtin", Name: "Cloud Accounts",
			Description: "Browse, edit, and save cloud credential profiles across AWS, GCP, and Azure.",
			Icon:        "key.fill", Category: CategoryDevOps,
		},
		{
			ID: LaunchTemplatesID, Version: "2.0.0", Publisher: "ezcloud",
			Engines: Engines{Ezcloud: ">=2.0"}, Clouds: []string{"aws"},
			Runtime: "builtin", Name: "Launch Templates",
			Description: "Edit EC2 launch templates like plain configs — clone, edit, apply as a new version, roll back.",
			Icon:        "server.rack", Category: CategoryDevOps,
		},
		{
			ID: TransferID, Version: "2.0.0", Publisher: "ezcloud",
			Engines: Engines{Ezcloud: ">=2.0"}, Clouds: []string{},
			Runtime: "builtin", Name: "Transfer",
			Description: "Export this profile to a .ezprofile file, or import one as a new profile.",
			Icon:        "square.and.arrow.up.on.square", Category: CategoryDevOps,
		},
	}
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
