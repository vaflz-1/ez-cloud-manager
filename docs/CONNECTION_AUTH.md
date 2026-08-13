# Connection Sign-In and Synchronization

Kervik delegates human authentication to the official cloud CLIs. The native
host coordinates discovery and an explicit review/apply flow, but it is not an
OAuth token store.

```text
user gesture → vendor browser login → token-free discovery → review → CAS apply
```

## Trust boundary

- Only the Platform Connections surface may request login. Add-ons cannot.
- Commands use exact argv and a constrained environment; no shell is involved.
- `aws`/`gcloud` are resolved only from absolute search paths owned by the
  current user or root. Writable binaries, foreign-owned links and unsafe
  writable search paths are rejected before execution. A group-writable
  package-manager directory may expose only a same-name symlink into a fully
  protected install tree; the discovery PATH is removed before the CLI starts.
- The vendor CLI owns access tokens, refresh tokens, device codes and browser
  callbacks. They never enter Swift models, the core JSON protocol, audit logs
  or profile files.
- Discovery returns bounded, non-secret descriptors and a SHA-256 snapshot
  revision. Apply re-discovers and rejects stale revisions.
- Per-connection writes use optimistic concurrency under the same file lock as
  the atomic write. A dirty editor or external CLI change becomes a conflict,
  never a silent overwrite.
- Batch GCP writes preflight every baseline before the first mutation and
  restore earlier rows if a later filesystem write fails.
- Cancelling login terminates the bundled core and its supervised vendor CLI
  process group. Apply is not terminated mid-batch; each result is explicit.

## AWS IAM Identity Center

Kervik discovers already configured IAM Identity Center profiles from the AWS
shared config and invokes `aws sso login` through AWS CLI v2. It does not read
`~/.aws/sso/cache`. SSO profiles are shown as provider-managed, read-only
Connections; Kervik never writes SSO keys to `~/.aws/credentials`.

Login and Test Connection receive a private, sanitized configuration snapshot
plus an empty credentials file. Delegated credential providers, custom
endpoints and TLS overrides are never copied; STS must return the account
declared by the reviewed profile.

The AWS Portal APIs for listing every entitled account and role require an SSO
bearer token. Under the no-token boundary Kervik therefore imports configured
local profiles. `aws configure sso` remains the trusted way to configure another
account/role.

## Google Cloud

Google Cloud has credentialed principals, projects and local named
configurations rather than AWS-style remote profiles. Kervik creates a temporary
non-active gcloud configuration, runs browser login against it, discovers
projects, then removes it. The user's global `active_config` and Application
Default Credentials are never changed by this flow.

Selected projects are created or updated as ordinary gcloud named
configurations. Existing region, zone and unknown properties are preserved.
Missing projects are never treated as deletion instructions.
Discovery and Test Connection also use temporary non-active configurations, so
impersonation, proxy, endpoint and custom-CA properties in mutable source files
cannot redirect the reviewed request.

## Repeat login modes

- **Update imported** — refresh known matches, without adding candidates.
- **Apply selected** — apply only checked rows.
- **Add new only** — create/link candidates that are not already present.

All three modes operate from the same reviewed snapshot. If the current
Workspace limits visible Connections, adding imported Connections to that scope
is a separate, explicit checkbox.
