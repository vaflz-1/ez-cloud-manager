# Kervik — Product DNA

Kervik is the working product name for a native, local-first control plane for
cloud and self-hosted work. It is not a dashboard and not a bag of provider
forms. The host stays small; connectors own trusted access; add-ons own useful
workflows.

> Calm command. Local by default. Fast by design.

## The promise

Open Kervik and immediately know:

1. which workspace is active;
2. which connections it can use;
3. which action is safe to take next.

Complexity should feel contained, never hidden. Credentials stay with trusted
connectors, network activity follows an explicit user action, and every
mutation has a visible result and audit trail.

## Product vocabulary

| User-facing term | Meaning | Legacy implementation term |
|---|---|---|
| Platform | The native host, broker, registry, policy and audit services | app / host |
| Workspace | One isolated operating context with env and enabled add-ons | global profile |
| Connection | A configured AWS account, GCP project, Azure subscription, device or self-hosted target | provider profile / account |
| Connector | A trusted adapter that owns discovery, auth, safe execution and redaction | provider rail |
| Add-on | A capability or workflow that uses typed platform and connector APIs | plugin |

Machine contracts remain compatible while Kervik is a working name:
`ezcloud`, `.ezprofile`, `EZCLOUD_*`, the bundle identifier and the existing
Application Support directory do not move without an explicit data migration.

## Visual language

The mark is one dark asymmetric monolith with one open threshold. It is an
independent symbol, not a cloud, gear, terminal, letter or provider logo.

- Mark: deep petrol ceramic on warm porcelain, with a restrained copper inner edge.
- Interface: semantic AppKit colors; the brand palette does not tint every control.
- Structure: one dominant hierarchy and deliberate negative space.
- Motion: 100–120 ms hover/fade only; no perpetual pulse, bounce or decorative scale.
- Small-size gate: the same silhouette must remain legible at 32 and 16 px.

## Interaction rules

- Workspace context is always visible.
- Connections and Add-ons are nouns; Save Changes is explicit.
- Switching rows never saves silently and never asks a generic overwrite question.
- Destructive operations identify the exact target and stay confirmable.
- A failed backend preserves the last good snapshot and reports its own error.
- Local reads never block the main thread; network work is cancellable and bounded.
- Keyboard, VoiceOver, Reduce Motion and Increased Contrast are product features.

## Performance contract

The measured Go core completes local reads in roughly 2–3 ms. The expensive
boundary is spawning it through Foundation Process (roughly 75–80 ms per call),
not the implementation language.

- First window target: 250 ms on a warm filesystem cache.
- Startup: one versioned bootstrap process, not a chain of local subprocesses.
- Connections refresh: one batched snapshot with per-connector errors.
- Release binaries: optimized and stripped; debug builds remain available.
- A persistent core is a later step, after request-scoped execution context and
  cancellation are designed. Rust is reserved for a measured CPU-bound leaf,
  not a rewrite of already-fast Go orchestration.

## Architecture boundary

Platform owns lifecycle, permissions, themes, profiles/workspaces, audit,
updates, rendering and the connection broker. Connectors own credentials and
safe typed cloud operations. Add-ons receive target IDs and typed results;
they never receive secret values, credential paths or a raw ambient process
environment.

The target source/runtime vocabulary is `platform/`, `connectors/`, `addons/`,
`sdk/` and `schemas/`. Existing compiled built-ins migrate incrementally; a
directory name alone does not count as a security boundary.
