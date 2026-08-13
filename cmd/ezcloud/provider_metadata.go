package main

import (
	"fmt"
	"sort"

	"ez-cloud-manager/internal/connector"
	"ez-cloud-manager/internal/provider"
)

// registeredProviderInfos joins runtime backends to their embedded connector
// contracts. The manifest owns catalog-facing name/icon metadata; the Go
// provider continues to own CRUD, schema and optional runtime capabilities.
func registeredProviderInfos() ([]providerInfo, error) {
	providerIDs := provider.IDs()
	manifests, err := matchConnectorManifests(providerIDs, connector.Builtins())
	if err != nil {
		return nil, err
	}

	infos := make([]providerInfo, 0, len(providerIDs))
	for _, id := range providerIDs {
		backend, err := provider.Get(id)
		if err != nil {
			return nil, err
		}
		manifest := manifests[id]
		info := providerInfo{
			ID:          id,
			DisplayName: manifest.Name,
			Icon:        manifest.Icon,
		}
		loginOperation := map[string]string{
			"aws": "aws.sso.login",
			"gcp": "gcp.identity.login",
		}[manifest.ID]
		syncOperation := map[string]string{
			"aws": "aws.sso.profiles.sync",
			"gcp": "gcp.configurations.sync",
		}[manifest.ID]
		for _, operation := range manifest.Provides.Operations {
			if operation == loginOperation {
				info.CanAuthenticate = true
			}
			if operation == syncOperation {
				info.CanSync = true
			}
		}
		if activator, ok := backend.(provider.Activator); ok {
			info.CanActivate = true
			info.ActivateLabel = activator.ActivateLabel()
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// matchConnectorManifests makes the compiled runtime/manifest boundary
// explicit. A manifest without a registered implementation is as misleading
// as a registered provider without package metadata, so both directions fail
// fast with deterministic diagnostics.
func matchConnectorManifests(providerIDs []string, manifests []connector.Manifest) (map[string]connector.Manifest, error) {
	byID := make(map[string]connector.Manifest, len(manifests))
	for _, manifest := range manifests {
		if _, duplicate := byID[manifest.ID]; duplicate {
			return nil, fmt.Errorf("duplicate connector manifest for registered provider %q", manifest.ID)
		}
		byID[manifest.ID] = manifest
	}

	registered := make(map[string]struct{}, len(providerIDs))
	missing := make([]string, 0)
	for _, id := range providerIDs {
		registered[id] = struct{}{}
		if _, ok := byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("registered providers missing connector manifests: %v", missing)
	}

	unregistered := make([]string, 0)
	for id := range byID {
		if _, ok := registered[id]; !ok {
			unregistered = append(unregistered, id)
		}
	}
	if len(unregistered) > 0 {
		sort.Strings(unregistered)
		return nil, fmt.Errorf("connector manifests missing registered providers: %v", unregistered)
	}
	return byID, nil
}
