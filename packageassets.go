// Package packageassets exposes the first-party package descriptors embedded
// in the application binary. Domain packages decode and validate their own
// manifests; keeping the embed boundary at the module root lets the physical
// addons/ and connectors/ trees remain the source of truth without import
// cycles or generated copies.
package packageassets

import (
	"embed"
	"io/fs"
)

// Only machine-readable package contracts are shipped in this filesystem.
// READMEs stay in the repository for authors and are not runtime inputs.
//
//go:embed addons/addon.schema.json addons/*/addon.json connectors/connector.schema.json connectors/*/connector.json
var embedded embed.FS

// Embedded returns a read-only view of the first-party package contracts.
func Embedded() fs.FS {
	return embedded
}
