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
// (accounts/env vars can also be added later via `profile save`).
type profileCreateRequest struct {
	Accounts []profile.AccountRef `json:"accounts"`
	EnvVars  []profile.EnvVar     `json:"envVars"`
}

// profileMgmtCommand manages global profiles — the platform-v2.0 container
// of cloud-account references, env vars and (later) plugin/settings state
// that one app window binds to (docs/PLATFORM.md phase P0). Named distinctly
// from profileCommand, which predates it and manages per-provider credential
// entries (--profile NAME) — an unrelated, unchanged concept.
func profileMgmtCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud profile list|show|create|save|rename|duplicate|delete|export|import|migrate"))
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
		p, err := profile.Create(root, profile.Profile{Name: *name, Accounts: req.Accounts, EnvVars: req.EnvVars})
		if err != nil {
			fail(err)
		}
		auditRecordKeys("profile-create", "", p.Name, nil)
		writeJSON(p)
	case "save":
		// Whole-object PUT (same convention as the legacy `ws save`): the
		// Profile Manager UI sends the full profile back, including any
		// Accounts/EnvVars edits.
		fs := flag.NewFlagSet("profile save", flag.ExitOnError)
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "profile save")

		var p profile.Profile
		if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&p); err != nil {
			fail(fmt.Errorf("read profile json: %w", err))
		}
		old, _ := profile.Get(root, p.ID)
		if err := profile.Save(root, p); err != nil {
			fail(err)
		}
		saved, err := profile.Get(root, p.ID)
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
	default:
		fail(fmt.Errorf("unknown profile subcommand %q", sub))
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
