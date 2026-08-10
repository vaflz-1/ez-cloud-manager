# Platform source map

Kervik's platform is the small trusted host around add-ons and connectors. It
owns app lifecycle, workspace/profile isolation, Connections, credential and
audit boundaries, package discovery, and the future permission broker.

The repository is in an explicit transition rather than a big-bang move:

- `ui/` is the native macOS host and system surfaces.
- `internal/profile`, `internal/provider` and provider credential packages are
  compiled platform services.
- `addons/*/addon.json` is the first-party add-on contract source of truth.
- `connectors/*/connector.json` is the first-party connectivity contract.
- Connections still has legacy ID `cloud-accounts` under `addons/connections`
  so existing profiles remain valid, but its manifest is `kind: "system"` and
  the product treats it as a platform-owned surface.

No manifest currently causes code to be dynamically loaded. Runtime remains
compiled (`runtime.type: "builtin"`) until the broker can enforce typed
operation grants and isolate native/declarative add-on execution. Source files
should move only as each broker boundary becomes real and tested.
