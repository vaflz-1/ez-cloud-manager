# Kervik (working name) — Roadmap

Positioning: **a fast, native, local-first platform for cloud and self-hosted
work.** The host owns Workspaces, policy, themes, audit and lifecycle;
Connectors own trusted access; Add-ons own operational workflows.

Kervik is not the final naming decision. Until that decision passes a separate
legal, ecosystem and domain screen, the repository, `ezcloud` CLI, bundle ID,
`.ezprofile`, `EZCLOUD_*` variables and existing data paths remain unchanged.
Branding must never force an unsafe data migration.

The detailed architecture and acceptance criteria live in
[PLATFORM.md](PLATFORM.md); interaction and visual decisions live in
[PRODUCT_DNA.md](PRODUCT_DNA.md).

## Current baseline — August 2026

- **P0 Workspaces — shipped.** Isolated operating contexts, explicit saves,
  compare-and-swap updates, saved/updated timestamps, import/export and
  one-window-per-Workspace lifecycle.
- **P1 Compiled Add-ons — shipped.** A native Add-on Hub, per-Workspace
  enablement, built-in workflows and a batched versioned bootstrap.
- **P2 Boundaries — foundation shipped.** Versioned Add-on and Connector
  manifests, an embedded registry, the permanent Connections surface,
  per-store locking and the fast isolated process bridge are present.

The manifests are real package contracts, but their implementations are still
compiled into the trusted application. Dynamic loading, sandboxing and the
typed permission broker are roadmap work, not current security claims.

## P3 — typed broker and declarative runtime

This is the next product milestone and the boundary that unlocks a genuine
ecosystem.

1. Build the ConnectionBroker: exact typed operation permissions, target
   resolution, minimal request-scoped environments, timeout/cancellation,
   redaction and auditable decisions.
2. Add schema-backed native views and settings for declarative Tier-1 Add-ons;
   do not expose shell command templates.
3. Implement stage → validate → verify → atomic install, plus an indexed active
   version and one verified rollback version.
4. Migrate Launch Templates from its compiled entrypoint as the first brokered
   AWS-dependent Add-on.
5. Keep Connections and Add-on management as non-removable Platform surfaces;
   migrate the legacy `cloud-accounts` state read-compatibly.

Exit gate: an Add-on can complete a useful cloud workflow without seeing a
credential value, credential path, raw ambient environment or unrestricted
command string.

## P4 — isolated external Add-ons

- Supervised JSON-RPC 2.0 processes with framed messages, bounded payloads,
  crash recovery and cancellation.
- A versioned Add-on SDK, local development mode and hot reload.
- Explicit permission receipts, signature identity, update review and atomic
  rollback.
- Declarative themes with no Connection permissions.

Arbitrary lifecycle hooks and injected dylibs remain out of scope.

## P5 — Connector ecosystem

- First-party signed Connectors for SSH, Kubernetes, local devices and
  self-hosted targets.
- Connector API compatibility ranges and contract suites independent of host
  release versions.
- Higher-trust installation and sandbox policy for external Connectors.
- DevSecOps, FinOps and AIOps Add-on packs built only on typed Connector APIs.

## Workflow candidates after the broker

These are Add-ons or Connector capabilities, not reasons to grow the Platform
core:

| Priority | Capability | Intended owner |
|---|---|---|
| 1 | SSO session status and explicit vendor-CLI re-login | AWS/GCP/Azure Connectors + session Add-on |
| 2 | Leapp, Granted and aws-vault import | migration Add-on through narrow import APIs |
| 3 | Command palette and menu-bar quick switch | Platform navigation over typed actions |
| 4 | Backup/audit history browser | privileged system Add-on over Workspace audit APIs |
| 5 | Kubernetes contexts, `.env` projects and Terraform workspaces | dedicated Connectors and Add-ons |
| 6 | SSM, Secrets Manager, GCP Secret Manager and Azure Key Vault | cloud-specific Add-ons over secret-store operations |
| 7 | Keychain and `credential_process` integration | trusted Connector storage layer |

## Product gates

- **Security:** no Add-on credential access; exact operation permissions;
  verified packages; atomic, locked and auditable mutations.
- **Performance:** warm first-window measurements track p50/p95; local core
  calls stay in the low-millisecond range; registries are indexed and Add-on
  views lazy-load.
- **Interaction:** Workspace context is always visible; Connections and
  Add-ons are stable nouns; saves are explicit; failures preserve the last
  good snapshot.
- **Accessibility:** keyboard, VoiceOver, Reduce Motion and Increased Contrast
  are release criteria, not polish tasks.
- **Distribution:** Developer ID and notarization precede Homebrew; an App
  Store build waits for a deliberate sandbox and security-scoped bookmark
  design.
- **Naming:** adopt a final public name only after collision screening and
  reserving the primary namespace; migrate display branding separately from
  machine and data identifiers.

## Explicit non-goals

- No hosted sync, SaaS account, telemetry or subscription requirement.
- No dashboard monolith and no cloud-specific workflow logic in the Platform.
- No raw credentials, OAuth refresh tokens or GCP private keys exposed to
  Add-ons.
- No Electron and no perpetual decorative animation.
- No Rust rewrite of already-fast Go orchestration. Rust is reserved for a
  measured CPU-bound leaf behind the same language-neutral API.
