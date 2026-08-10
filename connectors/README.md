# First-party connectors

Each directory describes one trusted Kervik connector. `connector.json` is the
stable connector contract; connection records refer to its `id`.

AWS, Google Cloud and Azure connectivity is currently compiled into the host.
The manifests establish the physical package boundary and discovery metadata,
but no connector is dynamically loaded and no add-on receives credentials
through them yet. That happens when the permissioned connector broker lands;
until then `runtime.type: "builtin"` is deliberate and truthful.

Connector manifests conform to `connector.schema.json`. `provides.operations`
is the typed API surface add-ons may request through the future broker; it is
not a list of shell commands. Manifests must never contain credentials, tokens
or machine-specific paths. `apiVersion` is the typed Connector API version and
is independent from the connector package version and the host engine range.
