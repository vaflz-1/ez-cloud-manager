package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"ez-cloud-manager/internal/audit"
	"ez-cloud-manager/internal/profile"
)

// migrateResponse is the JSON shape for `ezcloud profile migrate`.
type migrateResponse struct {
	Migrated int `json:"migrated"`
}

// profileCreateRequest is the optional stdin payload for `profile create`
// (env vars can also be added later via `profile save`; account scoping is
// the Cloud Accounts plugin's own settings blob — see `profile settings`).
type profileCreateRequest struct {
	EnvVars []profile.EnvVar `json:"envVars"`
}

// profileSaveRequest is intentionally limited to the fields owned by the
// Profile Manager. Older clients may still send a whole Profile; encoding/json
// ignores those extra fields and UpdateCore preserves the fresh plugin-owned
// state from disk.
type profileSaveRequest struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	EnvVars         []profile.EnvVar `json:"envVars"`
	ExpectedName    string           `json:"expectedName"`
	ExpectedEnvVars []profile.EnvVar `json:"expectedEnvVars"`
	// UpdatedAt is accepted from the legacy whole-Profile request shape as
	// a stricter fallback CAS when no explicit core snapshot is present.
	UpdatedAt string `json:"updatedAt"`
}

// profileMgmtCommand manages global profiles — the platform-v2.0 container
// of env vars and (later) plugin/settings state that one app window binds to
// (docs/PLATFORM.md phase P0). Named distinctly from profileCommand, which
// predates it and manages per-provider credential entries (--profile NAME)
// — an unrelated, unchanged concept.
func profileMgmtCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud profile list|show|create|save|rename|duplicate|delete|export|import|migrate|settings"))
	}
	root, err := profile.DefaultRoot()
	if err != nil {
		fail(err)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		fs := flag.NewFlagSet("profile list", flag.ExitOnError)
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile list")

		list, err := profile.List(root)
		if err != nil {
			fail(err)
		}
		writeJSON(list)
	case "show":
		fs := flag.NewFlagSet("profile show", flag.ExitOnError)
		id := fs.String("id", "", "profile id")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile show")

		p, err := profile.Get(root, requireProfileID(*id))
		if err != nil {
			fail(err)
		}
		writeJSON(p)
	case "create":
		fs := flag.NewFlagSet("profile create", flag.ExitOnError)
		name := fs.String("name", "", "profile name")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile create")
		if *name == "" {
			fail(fmt.Errorf("--name is required"))
		}

		req := readOptionalCreateRequest()
		p, err := profile.Create(root, profile.Profile{Name: *name, EnvVars: req.EnvVars})
		if err != nil {
			fail(err)
		}
		auditRecordKeys("profile-create", "", p.Name, nil)
		writeJSON(p)
	case "save":
		// Targeted core update: plugin enablement, plugin settings and window
		// state have separate writers and must survive a stale UI draft.
		fs := flag.NewFlagSet("profile save", flag.ExitOnError)
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile save")

		var req profileSaveRequest
		if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&req); err != nil {
			fail(fmt.Errorf("read profile json: %w", err))
		}
		old, err := profile.Get(root, req.ID)
		if err != nil {
			fail(err)
		}
		saved, err := profile.UpdateCore(root, profile.CoreUpdate{
			ID:                req.ID,
			Name:              req.Name,
			EnvVars:           req.EnvVars,
			ExpectedName:      req.ExpectedName,
			ExpectedEnvVars:   req.ExpectedEnvVars,
			ExpectedUpdatedAt: req.UpdatedAt,
		})
		if err != nil {
			fail(err)
		}
		auditRecordKeys("profile-save", "", saved.Name, changedEnvVarKeys(old.EnvVars, saved.EnvVars))
		writeJSON(saved)
	case "rename":
		fs := flag.NewFlagSet("profile rename", flag.ExitOnError)
		id := fs.String("id", "", "profile id")
		name := fs.String("name", "", "new profile name")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile rename")
		if *name == "" {
			fail(fmt.Errorf("--name is required"))
		}

		resolvedID := requireProfileID(*id)
		if err := profile.Rename(root, resolvedID, *name); err != nil {
			fail(err)
		}
		p, err := profile.Get(root, resolvedID)
		if err != nil {
			fail(err)
		}
		auditRecordKeys("profile-rename", "", p.Name, nil)
		writeJSON(p)
	case "duplicate":
		fs := flag.NewFlagSet("profile duplicate", flag.ExitOnError)
		id := fs.String("id", "", "profile id")
		name := fs.String("name", "", "new profile name (optional)")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile duplicate")

		p, err := profile.Duplicate(root, requireProfileID(*id), *name)
		if err != nil {
			fail(err)
		}
		auditRecordKeys("profile-duplicate", "", p.Name, nil)
		writeJSON(p)
	case "delete":
		fs := flag.NewFlagSet("profile delete", flag.ExitOnError)
		id := fs.String("id", "", "profile id")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile delete")

		resolvedID := requireProfileID(*id)
		old, _ := profile.Get(root, resolvedID)
		if err := profile.Delete(root, resolvedID); err != nil {
			fail(err)
		}
		auditRecordKeys("profile-delete", "", old.Name, nil)
		writeJSON(okResponse{OK: true})
	case "export":
		fs := flag.NewFlagSet("profile export", flag.ExitOnError)
		id := fs.String("id", "", "profile id")
		_ = fs.Parse(rest)
		// Raw zip bytes on stdout — the same "export is inherently raw"
		// precedent as the credential-entry `export` verb.
		if err := profile.Export(root, requireProfileID(*id), os.Stdout); err != nil {
			fail(err)
		}
	case "import":
		fs := flag.NewFlagSet("profile import", flag.ExitOnError)
		input := fs.String("input", "", "path to a .ezprofile file (- for stdin)")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile import")
		if *input == "" {
			fail(fmt.Errorf("--input is required"))
		}

		r, closeFn, err := openImportInput(*input)
		if err != nil {
			fail(err)
		}
		defer closeFn()
		p, err := profile.Import(root, r)
		if err != nil {
			fail(err)
		}
		auditRecordKeys("profile-import", "", p.Name, nil)
		writeJSON(p)
	case "migrate":
		fs := flag.NewFlagSet("profile migrate", flag.ExitOnError)
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile migrate")

		migrated, err := profile.MigrateFromWorkspaces(root)
		if err != nil {
			fail(err)
		}
		writeJSON(migrateResponse{Migrated: migrated})
	case "settings":
		profileSettingsCommand(rest, root)
	default:
		fail(fmt.Errorf("unknown profile subcommand %q", sub))
	}
}

// profileSettingsCommand is the generic (any plugin id) per-plugin settings
// accessor (docs/PLATFORM.md principle 5): `get` prints the raw JSON blob a
// profile carries for --plugin (or "{}" if it has none); `set` reads a raw
// JSON blob from stdin and stores only that namespace, returning the updated
// profile.
func profileSettingsCommand(args []string, root string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud profile settings get|set --id ID --plugin PLUGIN_ID"))
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("profile settings "+sub, flag.ExitOnError)
	id := fs.String("id", "", "profile id")
	pluginID := fs.String("plugin", "", "plugin id (settings namespace)")
	output := fs.String("output", "json", "output format (json only)")
	_ = fs.Parse(rest)
	requireJSONOutput(*output, "profile settings "+sub)
	resolvedID := requireProfileID(*id)
	if *pluginID == "" {
		fail(fmt.Errorf("--plugin is required"))
	}

	switch sub {
	case "get":
		p, err := profile.Get(root, resolvedID)
		if err != nil {
			fail(err)
		}
		blob := profile.GetSettingsBlob(p, *pluginID)
		if blob == nil {
			blob = json.RawMessage("{}")
		}
		os.Stdout.Write(blob)
	case "set":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
		if err != nil {
			fail(err)
		}
		saved, err := profile.SetSettingsBlob(root, resolvedID, *pluginID, raw)
		if err != nil {
			fail(err)
		}
		// Never the blob's contents — only which plugin's settings changed,
		// same "key names, never values" invariant as every other audit entry.
		auditRecordKeys("plugin-settings-save", "", saved.Name, []string{*pluginID})
		writeJSON(saved)
	default:
		fail(fmt.Errorf("unknown profile settings subcommand %q", sub))
	}
}

func requireProfileID(id string) string {
	if id == "" {
		fail(fmt.Errorf("--id is required"))
	}
	return id
}

// requireJSONOutput enforces --output json, satisfying PLATFORM.md's
// "headless parity per --output json" literally: no other output format is
// defined for these verbs (export is the sole raw-bytes exception, matching
// the credential-entry `export` verb's own precedent).
func requireJSONOutput(output, cmdName string) {
	if output != "json" {
		fail(fmt.Errorf("%s: unsupported --output %q (only json is supported)", cmdName, output))
	}
}

// readOptionalCreateRequest reads an optional stdin body for `profile
// create`. Unlike the credential-entry `save` verb, a body here is optional:
// an absent or empty one just means "no accounts or env vars yet".
func readOptionalCreateRequest() profileCreateRequest {
	var req profileCreateRequest
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
	if err != nil || len(data) == 0 {
		return req
	}
	_ = json.Unmarshal(data, &req)
	return req
}

func openImportInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}

// changedEnvVarKeys returns the sorted names of env var keys that were
// added, removed, or whose value changed — never values, per the audit
// package's invariant.
func changedEnvVarKeys(oldVars, newVars []profile.EnvVar) []string {
	oldMap := make(map[string]string, len(oldVars))
	for _, e := range oldVars {
		oldMap[e.Key] = e.Value
	}
	newMap := make(map[string]string, len(newVars))
	for _, e := range newVars {
		newMap[e.Key] = e.Value
	}
	return audit.ChangedKeys(oldMap, newMap)
}
