package main

import (
	"fmt"
	"sort"
	"time"

	"ez-cloud-manager/internal/plugin"
	"ez-cloud-manager/internal/profile"
	"ez-cloud-manager/internal/provider"
)

const appProtocolVersion = 1

// appBootstrapResponse is the single read-only snapshot the native shell
// needs before it can draw its first window. Keeping this versioned contract
// in one process call avoids paying Foundation.Process startup once per
// provider/schema/profile/addon while preserving the CLI's process isolation.
type appBootstrapResponse struct {
	ProtocolVersion int                         `json:"protocolVersion"`
	Providers       []providerInfo              `json:"providers"`
	Schemas         map[string]provider.Schema  `json:"schemas"`
	Profiles        []profile.Profile           `json:"profiles"`
	ActiveProfile   profile.Profile             `json:"activeProfile"`
	Addons          []pluginDescriptorWithState `json:"addons"`
}

// providerListResult deliberately returns errors per connector. One broken
// credential store must not erase the healthy providers from the Connections
// window, and a partial snapshot is more useful than a failed whole command.
type providerListResult struct {
	Provider    string                    `json:"provider"`
	DisplayName string                    `json:"displayName"`
	Path        string                    `json:"path,omitempty"`
	Profiles    []provider.ProfileSummary `json:"profiles"`
	Error       string                    `json:"error,omitempty"`
}

type connectionsListResponse struct {
	ProtocolVersion int                  `json:"protocolVersion"`
	Providers       []providerListResult `json:"providers"`
}

func appCommand(args []string) {
	if len(args) != 1 || args[0] != "bootstrap" {
		fail(fmt.Errorf("usage: ezcloud app bootstrap"))
	}
	root, err := profile.DefaultRoot()
	if err != nil {
		fail(err)
	}
	if _, err := profile.MigrateFromWorkspaces(root); err != nil {
		fail(err)
	}
	response, err := buildAppBootstrap(root)
	if err != nil {
		fail(err)
	}
	writeJSON(response)
}

func buildAppBootstrap(root string) (appBootstrapResponse, error) {
	profiles, err := profile.List(root)
	if err != nil {
		return appBootstrapResponse{}, err
	}
	if len(profiles) == 0 {
		return appBootstrapResponse{}, fmt.Errorf("no workspace profile exists after migration")
	}

	active := profiles[0]
	for _, candidate := range profiles[1:] {
		newer, err := isNewerProfile(candidate, active)
		if err != nil {
			return appBootstrapResponse{}, err
		}
		if newer {
			active = candidate
		}
	}

	infos, err := registeredProviderInfos()
	if err != nil {
		return appBootstrapResponse{}, err
	}
	providerIDs := provider.IDs()
	schemas := make(map[string]provider.Schema, len(providerIDs))
	for _, info := range infos {
		id := info.ID
		backend, err := provider.Get(id)
		if err != nil {
			return appBootstrapResponse{}, err
		}
		schemas[id] = backend.Schema()
	}

	enabled := make(map[string]bool, len(active.EnabledPlugins))
	for _, id := range active.EnabledPlugins {
		enabled[id] = true
	}
	descriptors := plugin.Descriptors()
	addons := make([]pluginDescriptorWithState, len(descriptors))
	for i, descriptor := range descriptors {
		addons[i] = pluginDescriptorWithState{
			Descriptor: descriptor,
			Enabled:    enabled[descriptor.ID],
		}
	}

	return appBootstrapResponse{
		ProtocolVersion: appProtocolVersion,
		Providers:       infos,
		Schemas:         schemas,
		Profiles:        profiles,
		ActiveProfile:   active,
		Addons:          addons,
	}, nil
}

func isNewerProfile(candidate, current profile.Profile) (bool, error) {
	candidateTime, err := time.Parse(time.RFC3339Nano, candidate.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("parse workspace %q updatedAt: %w", candidate.ID, err)
	}
	currentTime, err := time.Parse(time.RFC3339Nano, current.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("parse workspace %q updatedAt: %w", current.ID, err)
	}
	if candidateTime.Equal(currentTime) {
		return candidate.ID > current.ID, nil
	}
	return candidateTime.After(currentTime), nil
}

func connectionsCommand(args []string) {
	if len(args) != 1 || args[0] != "list" {
		fail(fmt.Errorf("usage: ezcloud connections list"))
	}

	infos, err := registeredProviderInfos()
	if err != nil {
		fail(err)
	}
	results := make([]providerListResult, 0, len(infos))
	for _, info := range infos {
		id := info.ID
		backend, err := provider.Get(id)
		if err != nil {
			results = append(results, providerListResult{
				Provider: id, Profiles: []provider.ProfileSummary{}, Error: err.Error(),
			})
			continue
		}
		result := providerListResult{
			Provider: id, DisplayName: info.DisplayName, Profiles: []provider.ProfileSummary{},
		}
		path, err := backend.DefaultPath()
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.Path = path
		profiles, err := backend.List(path)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		sort.Slice(profiles, func(i, j int) bool {
			return profiles[i].Name < profiles[j].Name
		})
		result.Profiles = profiles
		results = append(results, result)
	}

	writeJSON(connectionsListResponse{
		ProtocolVersion: appProtocolVersion,
		Providers:       results,
	})
}
