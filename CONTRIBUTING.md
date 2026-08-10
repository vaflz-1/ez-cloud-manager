# Contributing

Kervik is the working name for a local-first macOS Add-on platform with a Go
headless core and native AppKit host. Contributions should keep the Platform
small, predictable, fast and careful around credentials. Machine contracts
such as `ezcloud`, `.ezprofile`, `EZCLOUD_*` and existing data paths remain
compatible until an explicit migration is designed.

## Local Workflow

```bash
go test ./...
go test -race ./...
go vet ./...
swiftc -target "$(uname -m)-apple-macosx13.0" -warnings-as-errors \
  -typecheck ui/*.swift -framework AppKit
./build.sh
```

The build installs:

- `/Applications/Kervik.app` (or `$HOME/Applications/Kervik.app` without
  system write access)
- `$HOME/.local/bin/ezcloud` and the working-name alias
  `$HOME/.local/bin/kervik`

Run the process-bridge smoke suite against the freshly built core:

```bash
swiftc -warnings-as-errors \
  ui/FastProcessRunner.swift tools/FastProcessRunnerSmoke.swift \
  -o /tmp/kervik-fast-process-smoke
/tmp/kervik-fast-process-smoke ./dist/ezcloud
```

The app-scoped Workspace cache has its own deterministic stale-read gate:

```bash
swiftc -target "$(uname -m)-apple-macosx13.0" -warnings-as-errors \
  ui/Models.swift ui/FastProcessRunner.swift ui/CredentialsService.swift \
  tools/ProfileSnapshotSmoke.swift -o /tmp/kervik-profile-snapshot-smoke
/tmp/kervik-profile-snapshot-smoke
```

For changes that can affect startup, measure a distribution after `./build.sh`.
The first run is a warm-up and is excluded by the benchmark:

```bash
swiftc -warnings-as-errors -parse-as-library tools/AppLaunchBenchmark.swift \
  -framework CoreGraphics -o /tmp/kervik-app-launch-benchmark
/tmp/kervik-app-launch-benchmark \
  /Applications/Kervik.app/Contents/MacOS/EZCloudManager
```

## Guardrails

- Use Platform / Workspace / Connection / Connector / Add-on in user-facing
  copy. Legacy `profile`, `provider` and `plugin` names may remain in wire and
  storage contracts while they are read-compatible.
- Keep cloud workflows in Add-ons and trusted access in Connectors. An Add-on
  must receive typed results, never raw credentials, credential paths or the
  host's ambient environment.
- Do not add network calls unless an explicit user action requires them and
  the interface makes the target and effect clear.
- Do not log, upload or print credential values in tests, diagnostics or
  errors. Test credential writes with vendor paths redirected to temporary
  files.
- Preserve atomic writes, timestamped backups and audit events for mutations.
- Keep Workspace saves explicit; row selection must not silently persist
  changes or present a generic overwrite prompt.
- Prefer native AppKit controls and semantic system colors. Respect keyboard,
  VoiceOver, Reduce Motion and Increased Contrast.
- Benchmark performance-sensitive changes with warm-up plus p50/p95 samples;
  do not gate CI on one wall-clock observation.

## Good First Issues

- Add fixture-based compatibility tests for Add-on and Connector manifests.
- Improve VoiceOver labels, focus order and keyboard activation in a contained
  Platform or Add-on surface.
- Add cancellation and timeout coverage around a typed Connector operation.
- Improve secret masking and concealed-copy tests without exposing test data.
- Add Developer ID/notarization automation and a Homebrew cask.
