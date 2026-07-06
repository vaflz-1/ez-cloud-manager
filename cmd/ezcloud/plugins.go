package main

import (
	"flag"
	"fmt"

	"ez-cloud-manager/internal/plugin"
	"ez-cloud-manager/internal/profile"
)

// pluginDescriptorWithState is one registry entry annotated with whether a
// given profile currently has it enabled.
type pluginDescriptorWithState struct {
	plugin.Descriptor
	Enabled bool `json:"enabled"`
}

// pluginsCommand manages the plugin registry and per-profile enable state
// (docs/PLATFORM.md phase P1). P1's registry is fixed (plugin.Builtins()) —
// no install/remove yet, that's P2.
func pluginsCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud plugins list [--profile ID] | enable --profile ID --id ID | disable --profile ID --id ID"))
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
	default:
		fail(fmt.Errorf("unknown plugins subcommand %q", sub))
	}
}

// setPluginEnabled toggles one plugin id in a profile's EnabledPlugins and
// returns the whole updated Profile, so the Swift hub can post
// .profileDidChange with it exactly like any other whole-object save.
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
	if _, ok := plugin.ByID(*pluginID); !ok {
		fail(fmt.Errorf("unknown plugin %q", *pluginID))
	}

	p, err := profile.Get(root, *profileID)
	if err != nil {
		fail(err)
	}
	p.EnabledPlugins = toggled(p.EnabledPlugins, *pluginID, enabled)
	if err := profile.Save(root, p); err != nil {
		fail(err)
	}
	saved, err := profile.Get(root, *profileID)
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

// toggled returns ids with pluginID added (enabled) or removed (!enabled),
// never duplicated.
func toggled(ids []string, pluginID string, enabled bool) []string {
	out := make([]string, 0, len(ids)+1)
	found := false
	for _, id := range ids {
		if id == pluginID {
			found = true
			if !enabled {
				continue
			}
		}
		out = append(out, id)
	}
	if enabled && !found {
		out = append(out, pluginID)
	}
	return out
}
