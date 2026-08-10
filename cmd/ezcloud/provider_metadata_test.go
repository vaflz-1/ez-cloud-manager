package main

import (
	"reflect"
	"strings"
	"testing"

	"ez-cloud-manager/internal/connector"
	"ez-cloud-manager/internal/provider"
)

func TestRegisteredProviderInfosComeFromConnectorManifests(t *testing.T) {
	infos, err := registeredProviderInfos()
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := provider.IDs()
	if len(infos) != len(wantIDs) {
		t.Fatalf("provider infos = %d, want %d", len(infos), len(wantIDs))
	}
	for i, id := range wantIDs {
		manifest, ok := connector.ByID(id)
		if !ok {
			t.Fatalf("registered provider %q has no connector manifest", id)
		}
		if infos[i].ID != id || infos[i].DisplayName != manifest.Name || infos[i].Icon != manifest.Icon {
			t.Errorf("provider info[%d] = %+v, want metadata from %+v", i, infos[i], manifest)
		}
	}
}

func TestMatchConnectorManifestsCrossChecksBothDirections(t *testing.T) {
	manifest := connector.Manifest{ID: "aws"}

	got, err := matchConnectorManifests([]string{"aws"}, []connector.Manifest{manifest})
	if err != nil || !reflect.DeepEqual(got, map[string]connector.Manifest{"aws": manifest}) {
		t.Fatalf("match = %+v, %v", got, err)
	}

	_, err = matchConnectorManifests([]string{"aws", "gcp"}, []connector.Manifest{manifest})
	if err == nil || !strings.Contains(err.Error(), "missing connector manifests: [gcp]") {
		t.Fatalf("missing manifest error = %v", err)
	}

	_, err = matchConnectorManifests([]string{"aws"}, []connector.Manifest{manifest, {ID: "gcp"}})
	if err == nil || !strings.Contains(err.Error(), "missing registered providers: [gcp]") {
		t.Fatalf("unregistered manifest error = %v", err)
	}

	_, err = matchConnectorManifests([]string{"aws"}, []connector.Manifest{manifest, manifest})
	if err == nil || !strings.Contains(err.Error(), "duplicate connector manifest") {
		t.Fatalf("duplicate manifest error = %v", err)
	}
}
