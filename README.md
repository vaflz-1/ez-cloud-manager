# EZ Cloud Manager

macOS-first multi-cloud credential and config manager for people who juggle
clouds, accounts and clients all day. AWS profiles, Google Cloud
configurations and Azure service-principal profiles live in one fast native
window: inspect, edit, paste-to-parse, import, export, compare, switch.

EZ Cloud Manager is local-first, native, and boringly reliable: no hosted
sync, no SaaS login, no telemetry, no surprise network calls. Network is used
only when you explicitly work with EC2 Launch Templates (through your own AWS
CLI and credentials).

If it saves you time: [support it on Ko-fi ♥](https://ko-fi.com/vaflz).

## Providers

| Provider | Storage (native, CLI-compatible) | Highlights |
|---|---|---|
| AWS | `~/.aws/credentials` | full profile editor, SSO fields, EC2 Launch Templates, "copy to [default]" switch |
| Google Cloud | `~/.config/gcloud/configurations/config_*` | real gcloud named configurations, one-click activate (`active_config`) |
| Azure | `~/.config/ezcloud/azure_profiles.ini` (0600) | named profiles the az CLI never had; paste `az ad sp create-for-rbac` JSON directly |

All provider knowledge (field labels, secret masking, env-var names,
placeholders) is served by the CLI as a schema — the UI has no hardcoded
cloud logic, so new providers are pure Go additions.

## Features

- **Workspaces** — group (provider, profile) pairs per client/job/context;
  the sidebar popup scopes everything. References only, never credentials.
- **Paste-to-parse** — paste env lines (bash/PowerShell/Terraform `ARM_*`),
  INI blocks, `gcloud config list` output, service-account JSON or
  `az ad sp create-for-rbac` JSON; fields land in the editor. GCP private
  keys are deliberately never imported — keys stay in files.
- **Import / Export** — import any config file; export as shell `export`
  lines, `.env`, provider-native INI, or JSON. Clipboard exports use the
  concealed pasteboard type (clipboard managers won't log them); file
  exports are written 0600.
- **Compare** — field-level diff of two profiles, secrets masked.
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

- Go CLI core: `cmd/ezcloud` — JSON on stdout, one `--provider` flag
- Provider registry: `internal/provider` (+ `awsprovider`, `gcpprovider`,
  `azureprovider` adapters)
- Backends: `internal/awscreds`, `internal/gcpcreds`, `internal/azurecreds`,
  shared engine `internal/inifile`
- EC2 Launch Templates: `internal/awslt` (AWS CLI wrapper) +
  `internal/flatjson` (dotted-path flatten/unflatten)
- Workspaces / audit: `internal/workspace`, `internal/audit`
- Export renderers: `internal/export`
- Native macOS UI: `ui/*.swift` (AppKit, single module)

The UI talks to the CLI over JSON; the CLI is fully usable standalone:

```bash
ezcloud providers
ezcloud list --provider gcp
ezcloud export --provider azure --profile client-a --format env
pbpaste | ezcloud parse --provider azure
ezcloud lt templates --profile prod --region eu-west-1
ezcloud audit --limit 20
```

## Build / install

```bash
./build.sh    # builds Go CLI + Swift app, signs, installs to /Applications
go test ./...
```

## Security model

- Secrets stay in provider-native local files (interop with the real CLIs is
  the point); the app's own stores are 0600 under `~/.config/ezcloud/`.
- Nothing is logged or transmitted: no analytics, no crash reporting, and
  the audit log records key names only.
- Launch Template data (which can embed user-data secrets) is passed to the
  AWS CLI via a 0600 temp file, never argv.
- Destructive actions (delete profile, delete template version, overwrite
  `[default]`) always show an explicit, specific confirmation.

## Roadmap

See `docs/ROADMAP.md` for the prioritized plan (next integrations: SSM
Parameter Store, Secrets Manager, Kubernetes contexts, .env projects,
Terraform workspaces, menu-bar quick switcher, Keychain-backed storage).
