// Package azureprovider adapts the app-managed Azure profile store
// (internal/azurecreds) to the generic provider.Provider interface and
// registers it as "azure".
//
// Import for side effects to make the provider available:
//
//	import _ "ez-cloud-manager/internal/provider/azureprovider"
package azureprovider

import (
	"errors"

	"ez-cloud-manager/internal/azurecreds"
	"ez-cloud-manager/internal/provider"
)

const id = "azure"

type azureProvider struct{}

var _ provider.ConditionalSaver = azureProvider{}

// New returns the Azure profiles provider, backed by an app-owned 0600 INI
// file (the Azure CLI has no native named-profile store to reuse).
func New() provider.Provider { return azureProvider{} }

func (azureProvider) ID() string          { return id }
func (azureProvider) DisplayName() string { return "Azure" }

func (azureProvider) DefaultPath() (string, error) { return azurecreds.DefaultPath() }

func (azureProvider) List(path string) ([]provider.ProfileSummary, error) {
	summaries, err := azurecreds.List(path)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProfileSummary, len(summaries))
	for i, s := range summaries {
		out[i] = provider.ProfileSummary{Name: s.Name, Keys: s.Keys}
	}
	return out, nil
}

func (azureProvider) Get(path, name string) (provider.Profile, error) {
	p, err := azurecreds.Get(path, name)
	if err != nil {
		return provider.Profile{}, err
	}
	return provider.Profile{Name: p.Name, Fields: p.Fields}, nil
}

func (azureProvider) Save(path, name string, fields map[string]string) error {
	return azurecreds.Save(path, name, fields)
}

func (azureProvider) SaveIfUnchanged(path, name string, fields, expectedFields map[string]string, expectAbsent bool) error {
	err := azurecreds.SaveIfUnchanged(path, name, fields, expectedFields, expectAbsent)
	if errors.Is(err, azurecreds.ErrConflict) {
		return provider.ErrConnectionConflict
	}
	return err
}

func (azureProvider) Delete(path, name string) error {
	return azurecreds.Delete(path, name)
}

func (azureProvider) Parse(text string) provider.Parsed {
	p := azurecreds.Parse(text)
	return provider.Parsed{ProfileName: p.ProfileName, Fields: p.Fields, Notes: p.Notes}
}

// Schema exports the AZURE_* env spellings used by the Azure CLI and SDKs;
// Terraform's ARM_* spellings are accepted on import.
func (azureProvider) Schema() provider.Schema {
	return provider.Schema{
		Provider:    id,
		DisplayName: "Azure",
		Fields: []provider.FieldSpec{
			{Key: azurecreds.KeyTenantID, Display: "AZURE_TENANT_ID", Env: "AZURE_TENANT_ID", Common: true, Placeholder: "00000000-0000-0000-0000-000000000000"},
			{Key: azurecreds.KeyClientID, Display: "AZURE_CLIENT_ID", Env: "AZURE_CLIENT_ID", Common: true, Placeholder: "app registration (client) ID"},
			{Key: azurecreds.KeyClientSecret, Display: "AZURE_CLIENT_SECRET", Env: "AZURE_CLIENT_SECRET", Secret: true, Common: true, Placeholder: "Not set — click the eye to add"},
			{Key: azurecreds.KeySubscriptionID, Display: "AZURE_SUBSCRIPTION_ID", Env: "AZURE_SUBSCRIPTION_ID", Common: true, Placeholder: "00000000-0000-0000-0000-000000000000"},
			{Key: azurecreds.KeyCloud, Display: "AZURE_CLOUD_NAME", Env: "AZURE_CLOUD_NAME", Placeholder: "AzureCloud"},
			{Key: azurecreds.KeyLocation, Display: "location", Placeholder: "westeurope"},
			{Key: azurecreds.KeyResourceGroup, Display: "resource_group", Placeholder: "my-rg"},
		},
	}
}

func init() { provider.Register(New()) }
