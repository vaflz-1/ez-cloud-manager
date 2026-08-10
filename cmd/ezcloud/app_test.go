package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ez-cloud-manager/internal/connector"
	"ez-cloud-manager/internal/plugin"
	profilemodel "ez-cloud-manager/internal/profile"
	"ez-cloud-manager/internal/provider"
)

func TestAppBootstrapFreshInstallIsCompleteAndDeterministic(t *testing.T) {
	e := newCLIEnv(t)
	var response appBootstrapResponse
	e.runJSON(t, &response, "app", "bootstrap")

	if response.ProtocolVersion != appProtocolVersion {
		t.Fatalf("protocolVersion = %d, want %d", response.ProtocolVersion, appProtocolVersion)
	}
	if len(response.Profiles) != 1 || response.Profiles[0].Name != "Default" {
		t.Fatalf("profiles = %+v, want fresh Default", response.Profiles)
	}
	if response.ActiveProfile.ID != response.Profiles[0].ID {
		t.Fatalf("activeProfile = %q, want %q", response.ActiveProfile.ID, response.Profiles[0].ID)
	}
	wantProviderIDs := provider.IDs()
	if len(response.Providers) != len(wantProviderIDs) {
		t.Fatalf("providers = %d, want %d", len(response.Providers), len(wantProviderIDs))
	}
	for i, id := range wantProviderIDs {
		if response.Providers[i].ID != id {
			t.Fatalf("providers[%d].id = %q, want %q", i, response.Providers[i].ID, id)
		}
		manifest, ok := connector.ByID(id)
		if !ok {
			t.Fatalf("registered provider %q has no connector manifest", id)
		}
		if response.Providers[i].DisplayName != manifest.Name || response.Providers[i].Icon != manifest.Icon {
			t.Fatalf("providers[%d] metadata = %+v, want connector manifest name/icon", i, response.Providers[i])
		}
		if response.Schemas[id].Provider != id {
			t.Fatalf("schema[%q] missing or mismatched", id)
		}
	}
	if len(response.Addons) != len(plugin.Descriptors()) {
		t.Fatalf("addons = %d, want %d", len(response.Addons), len(plugin.Descriptors()))
	}
	for _, addon := range response.Addons {
		if addon.Enabled {
			t.Fatalf("fresh workspace unexpectedly enables addon %q", addon.ID)
		}
	}
}

func TestAppBootstrapSelectsMostRecentlyUpdatedWorkspaceAndAddonState(t *testing.T) {
	e := newCLIEnv(t)
	defaultID := e.migrateDefaultProfileID(t)
	var created struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &created, "profile", "create", "--name", "Current")
	e.run(t, "plugins", "enable", "--profile", created.ID, "--id", plugin.TransferID)

	var response appBootstrapResponse
	e.runJSON(t, &response, "app", "bootstrap")
	if response.ActiveProfile.ID == defaultID || response.ActiveProfile.ID != created.ID {
		t.Fatalf("activeProfile = %q, want newest %q", response.ActiveProfile.ID, created.ID)
	}
	for _, addon := range response.Addons {
		if addon.ID == plugin.TransferID && !addon.Enabled {
			t.Fatal("Transfer addon state was not included in bootstrap")
		}
	}
}

func TestIsNewerProfileParsesFractionalRFC3339InsteadOfComparingStrings(t *testing.T) {
	older := profilemodel.Profile{ID: "older", UpdatedAt: "2026-08-10T10:00:00.1Z"}
	newer := profilemodel.Profile{ID: "newer", UpdatedAt: "2026-08-10T10:00:00.100000001Z"}
	got, err := isNewerProfile(newer, older)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("nanosecond-later timestamp was not selected as newer")
	}
}

func TestConnectionsListUsesOneVersionedPartialSnapshot(t *testing.T) {
	e := newCLIEnv(t)
	cloudRoot := t.TempDir()
	gcloudPath := filepath.Join(cloudRoot, "gcloud-not-a-directory")
	if err := os.WriteFile(gcloudPath, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ezcloudBinary, "connections", "list")
	cmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
		"AWS_SHARED_CREDENTIALS_FILE="+filepath.Join(cloudRoot, "aws", "credentials"),
		"CLOUDSDK_CONFIG="+gcloudPath,
		"EZCLOUD_AZURE_PROFILES_FILE="+filepath.Join(cloudRoot, "azure", "profiles.ini"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("connections list: %v\n%s", err, out)
	}
	var response connectionsListResponse
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if response.ProtocolVersion != appProtocolVersion {
		t.Fatalf("protocolVersion = %d", response.ProtocolVersion)
	}
	wantIDs := provider.IDs()
	if len(response.Providers) != len(wantIDs) {
		t.Fatalf("providers = %d, want %d", len(response.Providers), len(wantIDs))
	}
	for i, id := range wantIDs {
		if response.Providers[i].Provider != id {
			t.Fatalf("providers[%d] = %q, want %q", i, response.Providers[i].Provider, id)
		}
		if response.Providers[i].Profiles == nil {
			t.Fatalf("providers[%d].profiles must encode as [], not null", i)
		}
	}
	var gcpResult *providerListResult
	for i := range response.Providers {
		if response.Providers[i].Provider == "gcp" {
			gcpResult = &response.Providers[i]
		}
	}
	if gcpResult == nil || gcpResult.Error == "" {
		t.Fatalf("gcp partial failure was not preserved: %+v", gcpResult)
	}
}
