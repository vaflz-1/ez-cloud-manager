# First-party add-ons

Each directory is one Kervik package. `addon.json` is the package contract;
its stable `id` is the namespace used by profiles and the CLI. Dependencies
are connector IDs in `requires.connectors`, and permissions are typed broker
operation IDs — never executable commands or environment-variable access.

These first-party add-ons are still compiled into the Go/Swift application.
The manifests now drive discovery metadata, but they do **not** make the
implementation dynamically loadable yet. Moving execution behind the
permissioned add-on broker is a later phase; until then
`runtime.type: "builtin"` is deliberate and truthful. `connections/` is a
transitional compatibility package with legacy ID `cloud-accounts` and
`kind: "system"`: Connections is a platform-owned surface, not a target
removable add-on.

New manifests must conform to `addon.schema.json`. Keep user-facing metadata
in the manifest and keep credentials or machine-specific configuration out of
the package tree. `engines.kervik` is canonical; `engines.ezcloud` remains a
temporary host-compatibility key while the CLI identity is migrated.
