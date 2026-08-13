# Kervik (working name)

Kervik is the working name for a fast, native, local-first platform for cloud
and self-hosted work. A small host provides isolated Workspaces, trusted
Connections and an Add-on surface; operational workflows stay outside the
core and can grow without turning the application into a dashboard monolith.

Kervik is deliberately boring at the trust boundary: no hosted Kervik account,
no cloud-side profile store, no telemetry, and no surprise network calls.
Network is used only for explicit Connector actions such as Sign In / Sync,
Test Connection or EC2 Launch Templates, through the official vendor CLIs.

Working-name compatibility is deliberate: the CLI remains `ezcloud`, exported
Workspaces remain `.ezprofile`, and existing bundle/data identifiers stay
unchanged until a dedicated migration exists. `kervik` is installed only as a
convenience alias.

If it saves you time: [support it on Ko-fi ♥](https://ko-fi.com/vaflz).

## Built-in Connectors

| Connector | Local storage (vendor-CLI compatible) | Connections and capabilities |
|---|---|---|
| AWS | `~/.aws/credentials` + read-only discovery from `~/.aws/config` | key-backed Connection editor, IAM Identity Center sign-in/sync, EC2 Launch Templates |
| Google Cloud | `~/.config/gcloud/configurations/config_*` | browser sign-in, project discovery, reviewed named-configuration sync, one-click activate |
| Azure | `~/.config/ezcloud/azure_profiles.ini` (0600) | named Connections the az CLI never had; paste `az ad sp create-for-rbac` JSON directly |

Connector knowledge (field labels, secret masking, environment names and
placeholders) is served by the headless core as a schema. The UI stays
cloud-agnostic; the current built-ins remain compiled while the typed broker
and external Connector runtime are introduced.

## Platform

- **Workspaces** — isolate one client/job/context, its non-secret environment
  and enabled Add-ons. The Workspace selector scopes everything. References
  only, never credentials.
- **Paste-to-parse** — paste env lines (bash/PowerShell/Terraform `ARM_*`),
  INI blocks, `gcloud config list` output, service-account JSON or
  `az ad sp create-for-rbac` JSON; fields land in the editor. GCP private
  keys are deliberately never imported — keys stay in files.
- **Connections** — manage AWS, Google Cloud and Azure contexts through
  connector-owned local stores; add-ons never need raw credentials.
- **Sign In / Sync** — on an explicit user action, AWS IAM Identity Center
  and Google Cloud open the system browser through their official CLIs. A
  token-free Add/Update/Unchanged preview appears before selected Connections
  are linked or written; OAuth/SSO tokens never cross the CLI boundary.
- **Import / Export** — import any config file; export as shell `export`
  lines, `.env`, Connector-native INI, or JSON. Clipboard exports use the
  concealed pasteboard type (clipboard managers won't log them); file
  exports are written 0600.
- **Compare** — field-level diff of two Connections, secrets masked.
- **Switch** — make a gcloud configuration active, or copy an AWS profile
  over `[default]`, always behind an explicit confirmation.
- **EC2 Launch Templates** — edit templates like a plain config. Under the
  hood the app follows the safe AWS practice: clone → edit → create new
  version → set default, with a diff confirmation before anything is
  written, one-click rollback of the default pointer, and version deletion
  only as a separate confirmed action. Requires the `aws` CLI.
- **Audit log** — every save/delete/activate/apply is appended to
  `~/.config/ezcloud/audit.log` with key *names* only, never values.
- **Safety rails** — atomic writes, timestamped backups (pruned, 0600),
  secret masking with per-row reveal, injection-safe INI validation.

## Architecture

- Go headless core and compatibility CLI: `cmd/ezcloud` — JSON on stdout;
  `--provider` remains the legacy wire name for selecting a Connector
- Embedded package contracts: `addons/*/addon.json`,
  `connectors/*/connector.json`, `packageassets.go`
- Manifest-driven Add-on registry: `internal/plugin` (implementations remain
  compiled until the permission broker exists)
- Transitional Connector registry: `internal/provider` (+ `awsprovider`,
  `gcpprovider`, `azureprovider` adapters); the package name is retained for
  wire compatibility during migration
- Backends: `internal/awscreds`, `internal/gcpcreds`, `internal/azurecreds`,
  shared engine `internal/inifile`
- EC2 Launch Templates: `internal/awslt` (AWS CLI wrapper) +
  `internal/flatjson` (dotted-path flatten/unflatten)
- Workspaces / audit: `internal/profile`, `internal/audit`
- Export renderers: `internal/export`
- Native macOS platform UI: `ui/*.swift` (AppKit, single module; transition
  map in `platform/README.md`)
- [Product/interaction contract](docs/PRODUCT_DNA.md)
- [Target Add-on/Connector architecture](docs/PLATFORM.md)
- [Connection sign-in and token boundary](docs/CONNECTION_AUTH.md)

The UI talks to the CLI over JSON; the CLI is fully usable standalone:

```bash
ezcloud providers
ezcloud list --provider gcp
ezcloud export --provider azure --profile client-a --format env
pbpaste | ezcloud parse --provider azure
ezcloud lt templates --profile prod --region eu-west-1
ezcloud audit --limit 20
ezcloud connections auth discover --provider aws
```

## Build / install

```bash
./build.sh    # builds, signs and installs /Applications/Kervik.app
go test ./...
```

## Security model

- Secrets stay in Connector-owned local files (interop with the real CLIs is
  the point); the app's own stores are 0600 under `~/.config/ezcloud/`.
- Nothing is logged or transmitted: no analytics, no crash reporting, and
  the audit log records key names only.
- AWS and Google remain the session owners. Kervik does not read AWS SSO
  caches, gcloud credential databases, device codes, access tokens or refresh
  tokens; the synchronization wire contains only profile/project metadata and
  hashed snapshot identifiers.
- Launch Template data (which can embed user-data secrets) is passed to the
  AWS CLI via a 0600 temp file, never argv.
- Destructive actions (delete Connection, delete template version, overwrite
  `[default]`) always show an explicit, specific confirmation.

## Roadmap

See the [roadmap](docs/ROADMAP.md) for the prioritized path from compiled
built-ins to the typed permission broker, declarative Add-ons, an isolated
external runtime and additional cloud, device and self-hosted Connectors.
