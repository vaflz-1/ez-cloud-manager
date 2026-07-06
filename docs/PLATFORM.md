# EZ Cloud Manager v2.0 — Platform Architecture

Status: **design accepted, pre-implementation** (July 2026).
Supersedes the monolith model of v1.x; v1.2–v1.4 roadmap items become
*plugins* on this platform instead of core features.

## Vision

A native macOS **platform for DevOps / DevSecOps / AIOps** — what VS Code
is to editing and Jenkins is to CI, this app is to cloud operations.
The application ships as a thin **skeleton**; every capability (EC2
templates, Lambda browser, IAM helper, k8s cluster monitor, even the
plugin manager itself) is a **plugin**. Everything a plugin contributes is
**declarative**, so any LLM — "even the crookedest one" — can generate,
install, and configure plugins for the user, with live UI refresh showing
which buttons/menus appear.

Goals, in the user's words:

- Feature parity with (and beyond) the vendor CLIs — expose what the
  console/CLI *can't* do comfortably: cross-region live views, visual
  diffs, copy-edit-recreate flows, one-click context switching.
- Don't fear growing the UI. The skeleton stays small; plugins grow it.
- Jenkins/VS Code-grade standards for plugin lifecycle and extension API.

## UX principles (binding, July 2026)

1. **Menu bar is minimal and basic.** Every menu-bar action must also be
   reachable from the in-window UI; menus mirror the interface, they never
   hold exclusive functionality.
2. **Empty skeleton first.** On first run the app looks deliberately
   empty: profile selector, settings, and a plugin hub that offers
   installing plugins. The platform identity must be visible in the first
   minute of use.
3. **Plugins open separately.** Each plugin presents as a module/card in
   the hub and opens in its own window or clearly bounded section — never
   silently woven into a monolithic main window.
4. **Low-code for DevOps.** The end state is a graphical environment
   covering the full vendor-CLI capability surface; every flow favors
   visual, declarative interaction over memorizing CLI incantations.
5. **Core owns no plugin data.** A core profile is exactly: name, env
   vars, enabled plugins, per-plugin settings blobs, window state.
   Anything domain-specific (e.g. the Cloud Accounts scoping list) lives
   in the owning plugin's settings namespace and is edited in that
   plugin's UI, never in the core profile editor.
6. **Errors are guided flows.** A blocked operation (e.g. deleting the
   active gcloud configuration) must offer the unblocking action in
   place, not a bare error dialog.

## Core vs. plugins

**Core (the skeleton)** contains ONLY:

| Area | Contents |
|---|---|
| Profile engine | Global profiles: accounts/creds refs, env vars, enabled plugins, settings; multi-window (one window ⇄ one profile) |
| Provider base | The 3 cloud adapters (aws/gcp/az CLI detection, auth/session state) — *rails*, no features |
| Plugin host | Manifest loader, contribution registry, declarative-view renderer, JSON-RPC runtime, hot reload |
| Settings | Light-IDE-style settings window; per-profile overrides |
| Security services | Credential broker, permission enforcement, audit log (existing `internal/audit`) |
| Bootstrap loader | Just enough to install the **Plugin Manager plugin** on first run |

Everything else — including the plugin manager / marketplace UI — is a
plugin. First-party seed plugins (installed by default, removable):

`plugin-manager` · `profiles-import` (Leapp/Granted/aws-vault) ·
`ec2-launch-templates` (extracted from v1.1) · `ec2-instances`
(all-regions live view) · `lambda-browser` · `iam-helper` ·
`sso-sessions` · `k8s-clusters` (contexts + live status) ·
`env-projects` (.env profiles) · `secrets-browser` (SSM/Secret
Manager/Key Vault).

## Plugin runtime — two tiers

### Tier 1: Declarative plugins (the default, AI-first)

A plugin is a directory with `plugin.json` (+ optional YAML view specs +
icons). **No code executes.** Behavior = command templates the core runs
through the credential broker; UI = view specs the core renders natively.
This is the tier any LLM can write; it is sandbox-safe by construction.

### Tier 2: Native plugins (escape hatch)

A separate executable speaking **JSON-RPC 2.0 over stdio** (HashiCorp
go-plugin / LSP pattern — no dylib loading, works with Go and anything
else). Handshake declares protocol version; core supervises the process
per profile. Required only for logic that can't be expressed
declaratively (streaming, computation, custom protocols).

## Manifest & contribution points

```json
{
  "id": "ec2-launch-templates",
  "version": "2.0.0",
  "publisher": "ezcloud",
  "engines": { "ezcloud": ">=2.0" },
  "clouds": ["aws"],
  "runtime": "declarative",
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
}
```

Contribution points (VS Code-style, rendered natively by core):

- **menus** — items in per-cloud menus and the ⌘K palette
- **sidebar** — sections/nodes in the sidebar tree
- **views** — declarative tables/detail panes/forms: columns, JSONPath
  bindings into CLI output, actions
- **settings** — JSON Schema → auto-rendered settings pane
- **commands** — actions bound to CLI templates (Tier 1) or RPC methods (Tier 2)
- **resources** — typed cloud resources with CRUD verb templates; core
  provides the generic list/watch/create/edit/delete UI, multi-region
  fan-out, and refresh. This single point powers "monitor everything
  running in all regions up to k8s cluster status".

## Credential broker (the security core)

Plugins **never see credentials**. A plugin submits a command template;
core (1) validates it against the plugin's `permissions.cli` allowlist,
(2) resolves the active profile's env/creds, (3) executes the vendor CLI
itself, (4) returns parsed JSON, (5) writes an audit entry
(who/plugin/profile/command/timestamp). Existing v1.x rules stand: tokens
never proxied, login always via vendor CLI, no telemetry, local-first.

Install-time security: permission manifest shown to the user
(Jenkins-style), registry entries carry sha256 + minisign signature;
Tier 2 binaries additionally require explicit user consent. DevSecOps
plugin designs (IAM audit, S3 posture, CloudTrail anomalies) draw on the
cloud-security skill library during development.

## Global profiles & multi-window

A **profile** = named bundle of: cloud accounts/creds references, env
var sets, enabled plugins (+versions), settings overrides, window/UI
state. Stored as one folder under
`~/Library/Application Support/EZCloudManager/profiles/<name>/` —
export/import = zip the folder or hand over a single `.ezprofile` file.
Rename/duplicate/edit with zero CLI friction.

- Every window is bound to exactly one profile (`File → New Window with
  Profile…`); two windows = two fully independent contexts (accounts,
  env, plugins, settings) side by side.
- Settings resolution: built-in defaults < app-global < profile.
- The v1.1 `internal/workspace` package is the seed of the profile
  engine; it gets promoted, not rewritten.

## Marketplace

- Registry = a git repo with `index.json` (Homebrew-tap model): id,
  versions, engines range, checksums, signature, category
  (DevOps/DevSecOps/AIOps/FinOps), clouds.
- The Plugin Manager plugin browses/searches/installs/updates; the same
  operations exist headless: `ezcloud plugin install|list|update|remove`.
- Third-party registries addable per profile (like VS Code galleries /
  Jenkins update centers). Bundled plugins can be selected at install.

## AI-native design

1. **Everything declarative + published JSON Schemas** for manifest,
   views, settings — an LLM writes a valid plugin from schema alone.
2. `ezcloud plugin scaffold <id>` generates a working skeleton;
   `ezcloud plugin validate <dir>` lints manifest/permissions/views.
3. **Hot reload with visible diff**: dropping/editing a plugin folder
   live-refreshes the UI and badges what changed ("+1 menu, +2 views") —
   the user watches buttons appear as the AI iterates.
4. `docs/llms.txt` + schema bundle shipped in-app so any assistant can be
   pointed at one URL/file to learn the whole extension API.
5. Headless parity: everything the UI can do exists as `ezcloud` CLI
   verbs with `--output json`, so agents can drive the app end-to-end.

## Migration map (v1.1 → v2.0)

| v1.1 asset | v2.0 destination |
|---|---|
| `internal/workspace` | Profile engine (core) |
| `internal/provider/{aws,gcp,azure}provider` | Provider base rails (core) |
| `internal/awscreds`,`gcpcreds`,`azurecreds`,`inifile` | Credential broker backends (core) |
| `internal/audit` | Audit service (core, extended with plugin attribution) |
| `internal/awslt` + `ui/LaunchTemplate*` | `ec2-launch-templates` plugin (first extraction, proves the API) |
| `internal/export`, `ui/AppDelegate+Transfer` | `profiles-import`/export plugin + `.ezprofile` format |
| `ui/AppDelegate+*` monolith | Split: core shell (window/profile/settings) vs. rendered contributions |
| `cmd/ezcloud` | Core daemon + `ezcloud plugin …` verbs |

## Phases

| Phase | Deliverable | Acceptance |
|---|---|---|
| **P0 Profiles** | Global profile engine, multi-window per profile, profile CRUD/rename/export/import UI, per-profile env vars | Two windows on two profiles with different env/accounts simultaneously |
| **P1 Plugin host** *(shipped July 2026)* | Plugin registry (compile-time "builtin" runtime), per-profile enable/disable, `ezcloud plugins` CLI, Plugin Hub shell (empty state + catalog), Cloud Accounts / Launch Templates / Transfer repackaged as separately-opening built-in plugins, menu-bar diet | Fresh profile → empty hub → enable from catalog → card appears → own window; two profiles hold independent plugin sets |
| **P2 Plugin manager** | Plugin-manager plugin, registry format, install/update/remove, permission consent UI, signatures | Install a plugin from a git registry through the UI; menu items appear live |
| **P3 AI layer** | Tier 1 declarative runtime (manifest loading, declarative renderer, credential broker enforcement, hot reload), schemas published, scaffold/validate CLI, llms.txt, live diff badges | `ec2-launch-templates` re-shipped as a Tier 1 declarative plugin; a fresh LLM session generates a working "S3 buckets" plugin from schemas alone |
| **P4 Resource fleet** | `resources` contribution point at scale: all-regions EC2 live view, k8s clusters status, Tier 2 runtime | Multi-region dashboards; first Tier 2 plugin (streaming) |
| **P5 DevSecOps pack** | IAM helper, S3/posture audit, secrets browsers, rotation reminders as marketplace plugins | Security pack passes its own audit-log review |

## Non-goals (unchanged from v1.x)

No SaaS/sync, no telemetry, no subscription, no Electron, no app-owned
token storage. The platform pivot changes *how features ship*, not the
local-first ethos.
