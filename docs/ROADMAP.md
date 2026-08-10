# Kervik — Roadmap

Positioning: **the fast, native, local-first multi-cloud config manager for
macOS.** Free + Ko-fi, no subscription, no telemetry, no Electron.

> **v2.0 platform pivot (July 2026):** the app becomes a thin skeleton
> (profiles + settings + plugin host) and every feature ships as a
> declarative, AI-generatable plugin — VS Code/Jenkins model for
> DevOps/DevSecOps/AIOps. Full design: [PLATFORM.md](PLATFORM.md).
> The v1.2–v1.4 items below remain valid but ship as *plugins* on that
> platform (phases P0–P5), not as core features.

## Why now — the market window (research, July 2026)

- **Both category leaders lost their backing.** Leapp's company (Noovolari)
  [officially shut down May 2024](https://blog.leapp.cloud/noovolari-has-officially-come-to-an-end/)
  (OSS limps on under beSharp, Pro/Team killed); Granted's company (Common
  Fate) [wound down](https://commonfate.io/blog/winding-down) and the tool
  was donated to community governance. Their users are actively hunting
  replacements — a migration wedge that is open *today*.
- **The paywall backlash precedent.** Lens went $14.90/user/mo → community
  revolt → OpenLens → FreeLens. [OrbStack](https://orbstack.dev) proved the
  winning macOS playbook: native Swift, instant launch, zero telemetry.
  That is exactly this app's architecture.
- **Validated pain points** (sources in the research notes): SSO
  session-expiry churn (#1 AWS CLI frustration), multi-account switching
  friction (a whole CLI-tool category exists for it),
  [Azure CLI still has no named profiles](https://learn.microsoft.com/en-us/answers/questions/517194/is-it-possible-to-login-multiple-azure-tenancies-u)
  (**shipped in v1.1**), gcloud wrong-project deploys, EC2 Launch Template
  versioning footguns ([immutable per version, by design](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/manage-launch-template-versions.html))
  (**shipped in v1.1**), .env sprawl (62% of secrets are duplicated —
  GitGuardian), plaintext tokens on disk.

## v1.2 — own the daily loop

| # | Feature | Why it wins | Effort |
|---|---|---|---|
| 1 | **Menu-bar quick switcher** (workspace + profiles, one-click activate, color-coded current context) | Switching is the highest-frequency action; the menu bar is the native superpower no CLI has. Includes the "which account/region am I in" indicator that prevents wrong-account disasters. | M |
| 2 | **Import from Leapp / Granted / aws-vault configs** | The migration wedge: capture refugees of the two dead market leaders while the window is open. Cheap (their stores are local files). | S |
| 3 | **SSO session status + one-click re-login** (expiry badge/countdown; runs `aws sso login` / `gcloud auth login` / `az login` via Terminal — tokens never touch the app) | Kills the single most-complained-about CLI pain. Local-first preserved: we trigger the vendor CLI, never proxy tokens. | M |
| 4 | **⌘K fuzzy palette** (search/switch/copy/export across all providers) | Granted's most-loved UX, natively; pure local, fast to build on existing state. | S |
| 5 | **History/backup browser** (audit timeline per profile + timestamped-backup restore) | The safety data already exists on disk; surfacing it turns invisible safety into visible product value. | S |

## v1.3 — widen the config surface

| # | Feature | Why | Effort |
|---|---|---|---|
| 6 | **kubeconfig contexts provider** (view/rename/switch/export `~/.kube/config` contexts) | Same mental model, huge audience overlap; stale-context-after-switching is a validated pain. | M |
| 7 | **.env project profiles** (register folders; their .env files get the same editor/diff/mask/export) | Extends "variables you hate editing" beyond clouds; attacks .env sprawl locally without SaaS creep. | S |
| 8 | **Open console in right account+region** (browser profile/container-aware deep links) | Granted's killer browser feature, GUI-native. | M |
| 9 | **Profile health checks** (offline lint: missing region, dangling source_profile, stale token; optional `sts get-caller-identity` test) | Catches "why is my CLI broken" before it burns an hour. | M |

## v1.4+ — depth, security, distribution

| # | Feature | Why | Effort |
|---|---|---|---|
| 10 | **SSM Parameter Store + Secrets Manager browser** (read/diff/edit via aws CLI, confirmations like LT flow) | Same promise as launch templates: config that is miserable in the CLI. | L |
| 11 | **GCP Secret Manager / Azure Key Vault** (same UX via gcloud/az) | Completes the big-three secrets story on the existing adapter rails. | L |
| 12 | **Keychain + `credential_process`** (secrets in Keychain, Touch ID reveal, AWS reads via helper) | Matches aws-vault's security bar without breaking CLI interop; revisits the earlier Keychain decision *with* the design it required. | L |
| 13 | **Rotation reminders** (native notifications for old static keys / expiring SP secrets) | Cheap hygiene nudge against the #9 validated pain. | S |
| 14 | **Raycast/Alfred/Shortcuts + global hotkey** | macOS power-user ecosystem fit. | S |
| 15 | **Distribution track**: Developer ID + notarization → Homebrew cask → (later) App Store build (needs sandbox redesign) | Notarization is the cheap 80% of distribution; App Store needs security-scoped bookmarks. | M |

## UI polish backlog (from the HIG design pass, July 2026)

Applied already: sidebar tree with TOOLS subheader, provider brand badges +
row dots + counts, PROFILE-card accent stripe, filter-hygiene empty states
with selection restore, hover-gated pulsing Ko-fi heart (85%→100% opacity).
Remaining, in order: restore keyboard shortcuts on plugin windows
(⌘N/⌘S/⌘R/⌘I/⌘E/⌘L lost in the P1 menu-bar diet — bind them on each
plugin window's own UI) · shared icon+title+body+button template for all empty
states · secret reveal cross-fade (0.12s) + eye hover tint · LT window
visual parity with the main window (unified toolbar, source-list templates,
"v3 (active)" green dot, "Apply as vN" prominent button) · Save as
bordered-prominent with ⌘S + Set Active with `bolt.fill` · heart click
particle burst (3-4 hearts, 0.6s float+fade) · Add/Remove as one segmented
control · active-profile green dot in sidebar rows.

## Explicit non-goals

- No hosted sync/SaaS, no telemetry, no subscription (contrast: Doppler
  $18–21/user/mo, Lens $14.90/user/mo — both fueled user resentment).
- No storing GCP private keys or OAuth refresh tokens in app-owned files;
  login always goes through the vendor CLI.
- No Electron. Ever. (Leapp's weight was a recurring complaint.)

## Competitive snapshot (July 2026)

| Tool | Platform | Price | Status / gap we exploit |
|---|---|---|---|
| Leapp | Electron, cross-platform | Free OSS; Pro/Team **discontinued** | Company shut down May 2024; heavy; AWS-centric; no config editing |
| Granted | CLI + browser ext | Free OSS | Company wound down; CLI-only, no GUI |
| aws-vault | CLI | Free OSS | Keychain/security bar to match; AWS-only, maintenance slowed |
| awsume / awsp | CLI | Free OSS | Switching only; no editing/import/export |
| Commandeer | Desktop GUI | Opaque freemium | "Cloud IDE" scope, AWS-centric, mixed polish |
| Lens / FreeLens / Aptakube | Desktop | $14.90/mo / free / $9/mo | Kubernetes-only; pricing-backlash precedent |
| Doppler / Infisical | SaaS | Free tier → ~$18+/user/mo | Cloud-hosted; the anti-thesis of local-first |
| Cyberduck / Transmit | macOS | Donation / ~$45 | Storage browsers, not config/creds |

Full research (competitor details, pain-point evidence with sources, 15
ranked candidates) is preserved in the session notes; key sources:
[Leapp](https://github.com/Noovolari/leapp) ·
[Noovolari shutdown](https://blog.leapp.cloud/noovolari-has-officially-come-to-an-end/) ·
[Common Fate winddown](https://commonfate.io/blog/winding-down) ·
[aws-vault](https://github.com/99designs/aws-vault) ·
[AWS SSO login pain](https://github.com/aws/aws-cli/discussions/9237) ·
[Azure no named profiles](https://github.com/Azure/azure-cli/issues/5402) ·
[gcloud configurations](https://docs.cloud.google.com/sdk/docs/configurations) ·
[LT immutability](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/manage-launch-template-versions.html) ·
[GitGuardian on .env sprawl](https://blog.gitguardian.com/secure-your-secrets-with-env/) ·
[OrbStack](https://orbstack.dev)
