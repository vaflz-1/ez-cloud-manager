package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"ez-cloud-manager/internal/plugin"
	"ez-cloud-manager/internal/profile"
)

// pluginDescriptorWithState is one registry entry annotated with whether a
// given profile currently has it enabled.
type pluginDescriptorWithState struct {
	plugin.Descriptor
	Enabled bool `json:"enabled"`
}

type pluginUpdateRequest struct {
	Changes map[string]bool `json:"changes"`
}

// pluginsCommand manages the plugin registry and per-profile enable state
// (docs/PLATFORM.md phase P1). P1's registry is fixed (plugin.Builtins()) —
// no install/remove yet, that's P2.
func pluginsCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud plugins list [--profile ID] | update --profile ID < changes.json | enable --profile ID --id ID | disable --profile ID --id ID"))
	}
	root, err := profile.DefaultRoot()
	if err != nil {
		fail(err)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		fs := flag.NewFlagSet("plugins list", flag.ExitOnError)
		profileID := fs.String("profile", "", "profile id (optional — omit for the bare registry)")
		output := fs.String("output", "json", "output format (json only)")
		_ = fs.Parse(rest)
		requireJSONOutput(*output, "plugins list")

		descriptors := plugin.Descriptors()
		if *profileID == "" {
			writeJSON(descriptors)
			return
		}
		p, err := profile.Get(root, *profileID)
		if err != nil {
			fail(err)
		}
		enabled := make(map[string]bool, len(p.EnabledPlugins))
		for _, id := range p.EnabledPlugins {
			enabled[id] = true
		}
		out := make([]pluginDescriptorWithState, len(descriptors))
		for i, d := range descriptors {
			out[i] = pluginDescriptorWithState{Descriptor: d, Enabled: enabled[d.ID]}
		}
		writeJSON(out)
	case "enable":
		setPluginEnabled(root, rest, true)
	case "disable":
		setPluginEnabled(root, rest, false)
	case "update":
		updatePlugins(root, rest)
	default:
		fail(fmt.Errorf("unknown plugins subcommand %q", sub))
	}
}

// updatePlugins applies a complete batch of requested enable/disable changes
// in one profile write and records one redacted audit event for the batch.
func updatePlugins(root string, args []string) {
	fs := flag.NewFlagSet("plugins update", flag.ExitOnError)
	profileID := fs.String("profile", "", "profile id")
	output := fs.String("output", "json", "output format (json only)")
	_ = fs.Parse(args)
	requireJSONOutput(*output, "plugins update")
	if *profileID == "" {
		fail(fmt.Errorf("--profile is required"))
	}

	var req pluginUpdateRequest
	if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&req); err != nil {
		fail(fmt.Errorf("read plugin changes json: %w", err))
	}
	if len(req.Changes) == 0 {
		fail(fmt.Errorf("changes must include at least one plugin"))
	}

	saved, changedIDs, err := applyPluginChanges(root, *profileID, req.Changes)
	if err != nil {
		fail(err)
	}
	auditRecordKeys("plugins-update", "", saved.Name, changedIDs)
	writeJSON(saved)
}

// setPluginEnabled keeps the legacy single-plugin CLI surface while routing
// its mutation through the same targeted batch path as `plugins update`.
func setPluginEnabled(root string, args []string, enabled bool) {
	fs := flag.NewFlagSet("plugins enable/disable", flag.ExitOnError)
	profileID := fs.String("profile", "", "profile id")
	pluginID := fs.String("id", "", "plugin id")
	output := fs.String("output", "json", "output format (json only)")
	_ = fs.Parse(args)
	requireJSONOutput(*output, "plugins enable/disable")
	if *profileID == "" || *pluginID == "" {
		fail(fmt.Errorf("--profile and --id are required"))
	}

	saved, _, err := applyPluginChanges(root, *profileID, map[string]bool{*pluginID: enabled})
	if err != nil {
		fail(err)
	}
	action := "plugin-disable"
	if enabled {
		action = "plugin-enable"
	}
	auditRecordKeys(action, "", saved.Name, []string{*pluginID})
	writeJSON(saved)
}

// applyPluginChanges validates the entire request before mutation, then hands
// the batch to internal/profile's targeted writer. Existing ids unknown to
// this build remain in the list; only ids explicitly present in changes are
// touched.
func applyPluginChanges(root, profileID string, changes map[string]bool) (profile.Profile, []string, error) {
	ids := make([]string, 0, len(changes))
	for id := range changes {
		if _, ok := plugin.ByID(id); !ok {
			return profile.Profile{}, nil, fmt.Errorf("unknown plugin %q", id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	saved, err := profile.UpdateEnabledPlugins(root, profileID, changes)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	return saved, ids, nil
}
