# Contributing

EZ Cloud Manager is a local-first macOS app with a Go core and native AppKit UI. Contributions should keep the app predictable, offline-friendly, and careful around credentials.

## Local Workflow

```bash
go test ./...
swiftc ui/EZCloudManager.swift -o /tmp/EZCloudManager.check -framework AppKit
./build.sh
```

The build installs:

- `/Users/octavian/Applications/EZ Cloud Manager.app`
- `/Users/octavian/.local/bin/ezcloud`

## Guardrails

- Do not add network calls unless the feature clearly requires them and the UI explains why.
- Do not log, upload, or print credential values in tests, diagnostics, or errors.
- Test credential writes with `AWS_SHARED_CREDENTIALS_FILE` pointed at a temporary file.
- Preserve timestamped backups before modifying credential files.
- Prefer native macOS controls and simple AppKit patterns over heavy dependencies.

## Good First Issues

- Add a diff preview before saving profile changes.
- Add search/filter for profile lists.
- Add support for GCP, Azure, Kubernetes contexts, or generic `.env` profiles.
- Improve secret masking and copy-to-clipboard controls.
- Add Homebrew cask and signed release packaging.
