package gcpprovider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ez-cloud-manager/internal/gcpcreds"
	"ez-cloud-manager/internal/provider"
)

func TestCheckUsesLiveProjectDescribeWhenProjectConfigured(t *testing.T) {
	root := t.TempDir()
	if err := gcpcreds.Save(root, "prod", map[string]string{
		gcpcreds.KeyAccount:                "user@example.invalid",
		gcpcreds.KeyProject:                "example-project",
		"auth.impersonate_service_account": "attacker@example.invalid",
		"proxy.address":                    "attacker.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "gcloud")
	script := `#!/bin/sh
test "$#" -eq 6 || exit 4
test "$1" = projects || exit 4
test "$2" = describe || exit 4
test "$3" = example-project || exit 4
test "$4" = --configuration || exit 4
case "$5" in kervik-check-*) ;; *) exit 4 ;; esac
test "$6" = '--format=json(projectId,projectNumber)' || exit 4
snapshot="$CLOUDSDK_CONFIG/configurations/config_$5"
/usr/bin/grep -q 'account = user@example.invalid' "$snapshot" || exit 4
/usr/bin/grep -q 'project = example-project' "$snapshot" || exit 4
if /usr/bin/grep -q 'impersonate\|proxy\|attacker' "$snapshot"; then exit 4; fi
printf '{"projectId":"example-project","projectNumber":"123"}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	result, err := New().(provider.Checker).Check(context.Background(), root, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Identity["verification"] != "live" {
		t.Fatalf("unexpected result: %+v", result)
	}
	leftovers, err := filepath.Glob(filepath.Join(root, "configurations", "config_kervik-check-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("isolated verification was not cleaned up: %v, %v", leftovers, err)
	}
}

func TestCheckRejectsMalformedSuccessfulGcloudOutput(t *testing.T) {
	root := t.TempDir()
	if err := gcpcreds.Save(root, "local", map[string]string{
		gcpcreds.KeyAccount: "user@example.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "gcloud")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'not-json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	result, err := New().(provider.Checker).Check(context.Background(), root, "local")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error != "gcloud returned malformed account JSON" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckRejectsDifferentActiveAccount(t *testing.T) {
	root := t.TempDir()
	if err := gcpcreds.Save(root, "local", map[string]string{
		gcpcreds.KeyAccount: "expected@example.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "gcloud")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '[{\"account\":\"other@example.invalid\",\"status\":\"ACTIVE\"}]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	result, err := New().(provider.Checker).Check(context.Background(), root, "local")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatalf("different principal was accepted: %+v", result)
	}
}
