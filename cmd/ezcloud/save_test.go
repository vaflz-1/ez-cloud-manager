package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"ez-cloud-manager/internal/provider"
)

func TestSaveWireContractRejectsStaleAndDuplicateCreate(t *testing.T) {
	e := newCLIEnv(t)
	runSave := func(name string, request saveRequest) (string, int) {
		t.Helper()
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(ezcloudBinary, "save", "--provider", "azure", "--profile", name)
		cmd.Env = append(os.Environ(),
			"EZCLOUD_DATA_DIR="+e.dataDir,
			"EZCLOUD_CONFIG_DIR="+e.configDir,
		)
		cmd.Stdin = bytes.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode()
		}
		if err != nil {
			t.Fatalf("run save: %v", err)
		}
		return string(out), 0
	}

	original := map[string]string{"tenant_id": "tenant-original"}
	if out, code := runSave("shared", saveRequest{Fields: original, ExpectAbsent: true}); code != 0 {
		t.Fatalf("initial conditional create: exit %d: %s", code, out)
	}
	updated := map[string]string{"tenant_id": "tenant-updated"}
	if out, code := runSave("shared", saveRequest{Fields: updated, ExpectedFields: original}); code != 0 {
		t.Fatalf("conditional update: exit %d: %s", code, out)
	}

	out, code := runSave("shared", saveRequest{
		Fields:         map[string]string{"tenant_id": "stale-overwrite"},
		ExpectedFields: original,
	})
	if code == 0 || !strings.Contains(out, provider.ConnectionConflictMarker) {
		t.Fatalf("stale update exit=%d output=%q, want stable conflict marker", code, out)
	}
	if out, code := runSave("shared", saveRequest{Fields: original, ExpectAbsent: true}); code == 0 || !strings.Contains(out, provider.ConnectionConflictMarker) {
		t.Fatalf("duplicate create exit=%d output=%q, want stable conflict marker", code, out)
	}

	var got provider.Profile
	e.runJSON(t, &got, "get", "--provider", "azure", "--profile", "shared")
	if got.Fields["tenant_id"] != "tenant-updated" {
		t.Fatalf("stale save overwrote current fields: %+v", got.Fields)
	}
}
