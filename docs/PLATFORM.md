# Kervik Platform Architecture

Status: **P0–P2 foundation: compiled runtime, physical package contracts;
permission broker and declarative loading are next** (August 2026).

Kervik is a native local control plane. The host is deliberately small;
Connections provide trusted access to clouds and self-hosted targets; Add-ons
provide the workflows users install. See [PRODUCT_DNA.md](PRODUCT_DNA.md) for
the product and interaction contract.

## Canonical model

| Layer | Owns | Must not own |
|---|---|---|
| Platform | native shell, Workspaces, Add-on lifecycle, Connections UI, permissions, themes, audit, updates, renderer and broker | provider-specific workflows |
| Connector | discovery, auth/session, credential-store integration, target resolution, safe environment, timeout/cancel, redaction and typed operations | EC2/Kubernetes/Terraform UX |
| Target | a configured AWS account, GCP project, Azure subscription, SSH host, device, cluster or self-hosted service | credentials exposed to Add-ons |
| Add-on | manifest, views, settings, workflows and requested connector operations | credential files, raw tokens or ambient environment |

User-facing vocabulary is Platform / Workspace / Connection / Connector /
Add-on. Legacy code and wire contracts still use `profile`, `provider` and
`plugin`; those names remain read-compatible during migration.

## Required boundary

An Add-on invokes a structured operation:

```json
{
  "addon": "ezcloud.ec2-launch-templates",
  "connector": "aws",
  "target": "target-id",
  "operation": "aws.ec2.launchTemplates.list",
  "input": { "region": "eu-west-1" },
  "timeoutMs": 15000
}
```

The broker checks the Add-on's declared operation permission, resolves the
target inside the Connector, constructs argv or an SDK request without a
shell, applies a minimal environment, redacts the result and audits the
decision. The Add-on receives typed JSON only. It never receives a token,
credential path or the host process environment.

Connector administration is a separate core-only API. Add-ons cannot call
raw credential `Get/Save/Delete`; the current `provider.Provider` interface is
therefore an implementation seed, not the public Add-on SDK.

## Source layout target

```text
app/macos/platform/                 # AppDelegate, shell, renderer, theme
cmd/kervik/                         # headless host (ezcloud compatibility alias)
internal/platform/
  addons/{registry,loader,runtime}/
  connections/{broker,policy,session}/
  profiles/
  security/{audit,secrets,signatures}/
  transport/jsonrpc/
addons/
  ec2-launch-templates/
    addon.json
    backend/ ui/ views/ resources/ tests/
  transfer/
    addon.json
    backend/ ui/ tests/
connectors/
  aws/    connector.json api.json runtime/ tests/
  gcp/    connector.json api.json runtime/ tests/
  azure/  connector.json api.json runtime/ tests/
sdk/{addonapi,connectorapi}/
schemas/{addon,connector,view,settings}.schema.json
```

Connections and Add-on management are non-removable Platform surfaces. The
current `cloud-accounts` built-in migrates into Connections. Launch Templates
becomes a real AWS-dependent Add-on. Transfer may remain a signed privileged
system Add-on, but only through a narrow Workspace export/import API.

The source tree now contains versioned `addons/` and `connectors/` contracts,
embedded through `packageassets.go`; `internal/plugin.Builtins()` validates
those manifests instead of duplicating their catalog metadata. Implementations
are still compiled from `internal/` and `ui/`, and Swift opens known built-in
entrypoints through a switch. That remaining runtime boundary is explicit:
manifest discovery is real, dynamic loading and sandboxing are not shipped yet.

Connections is exposed as a permanent Platform toolbar surface and its
transitional manifest is `kind: "system"`, so it is not offered as a removable
Add-on. The legacy `cloud-accounts` ID remains stored for profile compatibility.

## Package contracts

Example `addons/ec2-launch-templates/addon.json`:

```json
{
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
    "operations": [
      "aws.ec2.launchTemplates.read",
      "aws.ec2.launchTemplates.write"
    ]
  },
  "contributes": {}
}
```

Example `connectors/aws/connector.json`:

```json
{
  "$schema": "../connector.schema.json",
  "schemaVersion": 1,
  "id": "aws",
  "version": "2.0.0",
  "apiVersion": "1.0.0",
  "kind": "cloud",
  "publisher": "ezcloud",
  "engines": { "kervik": ">=2.0.0 <3.0.0", "ezcloud": ">=2.0.0 <3.0.0" },
  "runtime": { "type": "builtin", "entrypoint": "host:aws" },
  "name": "Amazon Web Services",
  "description": "Local AWS connection and credential storage.",
  "icon": "cloud.fill",
  "provides": {
    "operations": [
      "aws.credentials.read",
      "aws.credentials.write",
      "aws.credentials.delete",
      "aws.ec2.launchTemplates.read",
      "aws.ec2.launchTemplates.write"
    ]
  }
}
```

These examples describe the packages shipped today: metadata is
manifest-driven while implementations remain compiled and trusted. A future
Tier-1 package changes `runtime.type` to `declarative` and contributes
schema-backed views only after the renderer and permission broker ship.

`schemaVersion`, package `version`, host `engines` and Connector `apiVersion`
are independent. One canonical ID has one active version initially; the prior
verified version stays available for rollback. Workspace state stores enabled
IDs and settings only, never mutable package contents.

## Target runtime layout (not shipped yet)

```text
Kervik.app/Contents/Resources/
  Addons/ Connectors/ Schemas/
~/Library/Application Support/EZCloudManager/  # legacy path retained
  addons/<publisher>/<id>/<version>/
  connectors/<publisher>/<id>/<version>/
  profiles/<id>/profile.json
  cache/ logs/
```

Bundled packages will be trusted and signed with the app. User-installed
packages will be immutable: stage → validate schema → verify
checksum/signature → atomic rename → update an installer-generated index.
Startup will read the index, not recursively scan the filesystem. Relative
manifest paths must canonicalize inside the package root; symlink/path escape
must be rejected.

Third-party Connectors have a higher trust class than Add-ons. They remain a
later feature and run as signed supervised processes, never injected dylibs.

## Target runtime tiers

### Tier 1 — declarative by default

Manifest, JSON-schema views, settings and typed connector operations. No
package code executes. Themes are declarative contributions with zero
Connection permissions.

Tier 1 does **not** expose raw CLI command templates. The Connector builds
commands or SDK calls after the broker authorizes a typed operation.

### Tier 2 — isolated process

A long-lived executable speaks versioned JSON-RPC 2.0 using `Content-Length`
framing over stdio. This is LSP-style transport, not HashiCorp go-plugin.
The Platform supplies a narrow cwd and environment, payload/resource limits,
cancellation, crash supervision and explicit permission consent. Arbitrary
lifecycle hooks are out of scope.

## Workspaces and persistence

A Workspace contains name, non-secret env vars, enabled Add-on IDs, opaque
per-Add-on settings and window state. One window binds to one Workspace.
Core saves patch name/env with compare-and-swap; Add-on enablement and each
settings namespace have targeted writers. `savedAt` changes only on explicit
Workspace save; `updatedAt` tracks every persisted mutation.

Package contents are never profile state. Connector secrets stay in native
stores/Keychain and are referenced through target IDs.

## Target security invariants

The broker/installer invariants below are acceptance criteria for the next
runtime phase, not claims about the current compiled built-ins:

- No telemetry or hosted sync by default.
- No Add-on receives credentials, credential paths or raw ambient env.
- No shell interpolation; Connector owns structured argv/request building.
- Every process has timeout/cancel and bounded payloads.
- Mutations are atomic, locked per store and auditable with Add-on, Connector,
  target, decision, result and duration metadata.
- Add-on permissions are exact typed operations, not wildcard command strings.
- Install/update verifies schema, checksum, signature and package-root paths.
- Tier 2 and external Connectors require explicit consent and sandbox policy.

Already shipped foundations: profile JSON, all three credential backends, and
audit rotation use stable cross-process locks around full mutation
transactions. Reads remain lock-free over atomically replaced files.

## Performance model

The Go core is not the measured bottleneck. Local commands take roughly
2–3 ms. The former Swift Foundation Process bridge cost roughly 65–80 ms;
the current `posix_spawn` bridge runs a warm real bootstrap at roughly 3 ms
while preserving one isolated process and request-scoped environment per
call. Therefore:

- `app bootstrap` returns migration, providers, schemas, Workspaces, selected
  Workspace and initial Add-on state in one versioned snapshot;
- `connections list` returns every Connector in one process with per-provider
  errors;
- batched Connection reads run off the main thread and stale completions use
  generation IDs; remaining short local mutations are migrated incrementally;
- external-package startup will read a validated registry index and lazy-load
  Add-on views after the dynamic runtime exists;
- a persistent core comes after request-scoped execution context exists.

Rust is not a platform rewrite target. It is allowed for a profiled CPU-bound
leaf (large state parsing, streaming logs, crypto or a sandbox runtime) behind
the same language-neutral API.

## Migration phases

1. **P0 Workspaces — shipped.** Isolated contexts, explicit saves, CAS,
   timestamps, import/export and one-window-per-Workspace lifecycle.
2. **P1 Compiled Add-ons — shipped.** Hub, per-Workspace enablement and three
   separately-opening built-ins. Bootstrap batching is the first performance
   foundation.
3. **P2 Boundaries — foundation shipped.** First-party manifests, embedded
   registry, system Connections surface and per-store locks are present;
   ConnectionBroker remains the next security boundary.
4. **P3 Declarative runtime.** Typed Connector broker APIs, native view renderer,
   installer/signature/index flow; migrate Launch Templates as the proof.
5. **P4 External runtime.** Supervised JSON-RPC Add-ons, SDK, hot reload in dev,
   permission receipts and rollback.
6. **P5 Connector ecosystem.** Signed SSH, Kubernetes, local-device and
   self-hosted Connectors plus DevSecOps/FinOps Add-on packs.

Compatibility aliases `plugins`, `enabledPlugins`, `ezcloud`, `EZCLOUD_*` and
`.ezprofile` remain readable for at least one major migration cycle.
