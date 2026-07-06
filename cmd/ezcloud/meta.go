package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"ez-cloud-manager/internal/audit"
	"ez-cloud-manager/internal/workspace"
)

// wsCommand manages named workspaces — app-level groupings of
// (provider, profile) references. No secrets live here.
func wsCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud ws list | save | delete --name NAME | rename --old A --new B"))
	}
	path, err := workspace.DefaultPath()
	if err != nil {
		fail(err)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		list, err := workspace.List(path)
		if err != nil {
			fail(err)
		}
		writeJSON(list)
	case "save":
		// Full workspace JSON on stdin: {"name":"…","members":[{"provider":"aws","profile":"x"},…]}
		var w workspace.Workspace
		if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&w); err != nil {
			fail(fmt.Errorf("read workspace json: %w", err))
		}
		if err := workspace.Save(path, w); err != nil {
			fail(err)
		}
		auditRecord("workspace-save", "", w.Name, nil, nil)
		writeJSON(okResponse{OK: true})
	case "delete":
		fs := flag.NewFlagSet("ws delete", flag.ExitOnError)
		name := fs.String("name", "", "workspace name")
		_ = fs.Parse(rest)
		if *name == "" {
			fail(fmt.Errorf("--name is required"))
		}
		if err := workspace.Delete(path, *name); err != nil {
			fail(err)
		}
		auditRecord("workspace-delete", "", *name, nil, nil)
		writeJSON(okResponse{OK: true})
	case "rename":
		fs := flag.NewFlagSet("ws rename", flag.ExitOnError)
		old := fs.String("old", "", "current workspace name")
		newName := fs.String("new", "", "new workspace name")
		_ = fs.Parse(rest)
		if *old == "" || *newName == "" {
			fail(fmt.Errorf("--old and --new are required"))
		}
		if err := workspace.Rename(path, *old, *newName); err != nil {
			fail(err)
		}
		writeJSON(okResponse{OK: true})
	default:
		fail(fmt.Errorf("unknown ws subcommand %q", sub))
	}
}

// auditCommand prints recent audit events (redacted by construction: the
// audit package stores key names and metadata, never values).
func auditCommand(args []string) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	limit := fs.Int("limit", 50, "maximum number of events")
	_ = fs.Parse(args)
	path, err := audit.DefaultPath()
	if err != nil {
		fail(err)
	}
	events, err := audit.List(path, *limit)
	if err != nil {
		fail(err)
	}
	writeJSON(events)
}

// auditRecord appends a redacted audit event. Best-effort by design: an
// unwritable audit log must never block a credentials operation, but the
// failure is surfaced on stderr rather than swallowed.
func auditRecord(action, providerID, profile string, oldFields, newFields map[string]string) {
	var keys []string
	if oldFields != nil || newFields != nil {
		keys = audit.ChangedKeys(oldFields, newFields)
	}
	auditRecordKeys(action, providerID, profile, keys)
}

// auditRecordKeys is auditRecord for callers that already know which key
// NAMES changed (values are never logged).
func auditRecordKeys(action, providerID, profile string, keys []string) {
	path, err := audit.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: audit log unavailable: %v\n", err)
		return
	}
	event := audit.Event{
		Action:   action,
		Provider: providerID,
		Profile:  profile,
		Keys:     keys,
	}
	if err := audit.Append(path, event); err != nil {
		fmt.Fprintf(os.Stderr, "warning: audit append failed: %v\n", err)
	}
}
