# EZ Cloud Manager

macOS-first cloud credential and config manager. Current release focuses on AWS credential profiles in `~/.aws/credentials`; the app shell is ready for more cloud/account workflows later.

EZ Cloud Manager is meant to be local-first, native, and boringly reliable: no hosted sync, no SaaS login, no surprise network calls. The first goal is making cloud credentials easier to inspect, paste, update, and rotate from a small native macOS app.

Architecture:

- Go CLI core: `cmd/ezcloud`
- Native macOS UI: `ui/EZCloudManager.swift`
- macOS app icon source: `tools/generate-icon.swift`
- Installed app: `/Users/octavian/Applications/EZ Cloud Manager.app`
- CLI symlink: `/Users/octavian/.local/bin/ezcloud`
- Legacy CLI aliases: `/Users/octavian/.local/bin/cloudez`, `/Users/octavian/.local/bin/awspm`

Features:

- List profiles from `~/.aws/credentials`
- Add a new profile
- Update an existing profile in-place
- Delete a profile
- Paste an AWS credentials/config block and auto-parse it
- Review/edit env-style variables and common AWS profile fields in an always-visible native table before saving
- Let the variables table grow and shrink naturally when the macOS window is resized
- Save profile fields back to the AWS credentials file
- Create a timestamped backup before writing

Build/install:

```bash
/Users/octavian/Documents/software/ez-cloud-manager/build.sh
```

CLI examples:

```bash
ezcloud list
ezcloud get --profile default
pbpaste | ezcloud parse
```

Auto-parsed fields include access keys, session token, profile name, default region/output, role/source profile fields, credential process/source, web identity token file, endpoint URL, retry settings, and common SSO profile keys.

Good first contribution areas:

- Providers: Azure, GCP, Kubernetes contexts, generic `.env` profiles
- Safety: diff preview before writing credentials, better secret masking, backup browser
- UX: profile search, keyboard shortcuts, import/export, menu bar helper
- Distribution: notarized release build, Homebrew cask, signed GitHub releases

See `CONTRIBUTING.md` for local build/test notes and project guardrails.

Notes:

- The app is local-only and does not call AWS or any network service.
- `AWS_SHARED_CREDENTIALS_FILE` is respected by the Go CLI if set.
- Writes are atomic and `~/.aws/credentials.bak.YYYYMMDD-HHMMSS` is created before changes.
