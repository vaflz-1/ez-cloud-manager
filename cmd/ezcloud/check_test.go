package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ez-cloud-manager/internal/audit"
	profilemodel "ez-cloud-manager/internal/profile"
	"ez-cloud-manager/internal/provider"
)

// runWithPath is cliEnv.run but with PATH pinned to dir instead of inherited
// from the test process — the only way to force a deterministic "vendor CLI
// not found" or "vendor CLI hangs" outcome regardless of what happens to be
// installed on the machine running this test, and WITHOUT ever touching a
// real aws/gcloud install or making a real network call.
func (e cliEnv) runWithPath(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(ezcloudBinary, args...)
	cmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
		"PATH="+dir,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return outBuf.String(), errBuf.String(), code
}

// seedCheckWorkspace gives the sensitive `check` verb both halves of its
// authorization contract: a real Connection in the Workspace-resolved store
// and an explicit grant to that existing identity. Vendor binaries remain
// isolated by runWithPath, so these fixtures never touch live cloud state.
func seedCheckWorkspace(t *testing.T, e cliEnv, providerID, connectionName string) string {
	t.Helper()
	var (
		store  string
		env    []profilemodel.EnvVar
		fields map[string]string
	)
	switch providerID {
	case "aws":
		store = filepath.Join(e.configDir, "check-aws-credentials")
		env = []profilemodel.EnvVar{{Key: "AWS_SHARED_CREDENTIALS_FILE", Value: store}}
		fields = map[string]string{
			"aws_access_key_id":     "AKIAEXAMPLE",
			"aws_secret_access_key": "not-a-real-secret",
		}
	case "gcp":
		store = filepath.Join(e.configDir, "check-gcloud")
		env = []profilemodel.EnvVar{{Key: "CLOUDSDK_CONFIG", Value: store}}
		fields = map[string]string{"core.account": "nobody@example.invalid"}
	case "azure":
		store = filepath.Join(e.configDir, "azure_profiles.ini")
		fields = map[string]string{"tenant_id": "tenant-example"}
	default:
		t.Fatalf("unsupported check fixture provider %q", providerID)
	}

	backend, err := provider.Get(providerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Save(store, connectionName, fields); err != nil {
		t.Fatalf("seed %s Connection: %v", providerID, err)
	}
	root := filepath.Join(e.dataDir, "profiles")
	workspace, err := profilemodel.Create(root, profilemodel.Profile{
		Name:    "check-" + providerID,
		EnvVars: env,
	})
	if err != nil {
		t.Fatalf("create check Workspace: %v", err)
	}
	if _, err := profilemodel.AddConnectionRef(root, workspace.ID, profilemodel.AccountRef{
		Provider: providerID,
		Account:  connectionName,
	}); err != nil {
		t.Fatalf("grant check Connection: %v", err)
	}
	return workspace.ID
}

func TestCheckAzureReportsUnsupportedCleanly(t *testing.T) {
	e := newCLIEnv(t)
	workspaceID := seedCheckWorkspace(t, e, "azure", "anything")
	var result provider.CheckResult
	e.runJSON(t, &result, "check", "--provider", "azure", "--workspace", workspaceID, "--profile", "anything")
	if result.OK {
		t.Fatal("azure has no Checker yet — expected ok=false")
	}
	if result.Error == "" {
		t.Fatal("expected a human-readable 'not supported' error")
	}
	if result.Identity != nil {
		t.Fatalf("identity should be absent when not ok: %+v", result.Identity)
	}
}

func TestCheckAwsMissingBinaryReportsCleanlyNeverRealCall(t *testing.T) {
	e := newCLIEnv(t)
	emptyBin := t.TempDir() // deliberately has no "aws" — forces the failure
	workspaceID := seedCheckWorkspace(t, e, "aws", "does-not-exist")
	stdout, stderr, code := e.runWithPath(t, emptyBin, "check", "--provider", "aws", "--workspace", workspaceID, "--profile", "does-not-exist")
	if code != 0 {
		t.Fatalf("a missing vendor CLI must still be a clean CheckResult (exit 0), got exit %d, stderr: %s", code, stderr)
	}
	var result provider.CheckResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout)
	}
	if result.OK {
		t.Fatal("expected ok=false with no aws binary on PATH")
	}
}

func TestCheckGcpMissingBinaryReportsCleanlyNeverRealCall(t *testing.T) {
	e := newCLIEnv(t)
	emptyBin := t.TempDir()
	workspaceID := seedCheckWorkspace(t, e, "gcp", "does-not-exist")
	stdout, stderr, code := e.runWithPath(t, emptyBin, "check", "--provider", "gcp", "--workspace", workspaceID, "--profile", "does-not-exist")
	if code != 0 {
		t.Fatalf("a missing vendor CLI must still be a clean CheckResult (exit 0), got exit %d, stderr: %s", code, stderr)
	}
	var result provider.CheckResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout)
	}
	if result.OK {
		t.Fatal("expected ok=false with no gcloud binary on PATH")
	}
}

func TestCheckUnknownProviderRejected(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")
	_, stderr, code := e.run(t, "check", "--provider", "nonexistent-cloud", "--workspace", workspace.ID, "--profile", "x")
	if code == 0 {
		t.Fatal("expected a non-zero exit for an unknown provider")
	}
	if stderr == "" {
		t.Fatal("expected an error message on stderr")
	}
}

func TestCheckMissingProfileRejected(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")
	_, stderr, code := e.run(t, "check", "--provider", "aws", "--workspace", workspace.ID)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a missing --profile")
	}
	if stderr == "" {
		t.Fatal("expected an error message on stderr")
	}
}

func TestCheckMissingWorkspaceRejected(t *testing.T) {
	e := newCLIEnv(t)
	_, stderr, code := e.run(t, "check", "--provider", "aws", "--profile", "connection")
	if code == 0 || !strings.Contains(stderr, "--workspace is required") {
		t.Fatalf("unscoped check exit=%d stderr=%q, want mandatory Workspace rejection", code, stderr)
	}
}

// TestCheckAuditEntryNeverIncludesIdentityOrErrorText is the "no secret
// material in output or audit" requirement: the audit log must record only
// the action/provider/profile, never CheckResult.Identity or .Error text
// (which could contain an account number, ARN, or vendor-CLI error output).
func TestCheckAuditEntryNeverIncludesIdentityOrErrorText(t *testing.T) {
	e := newCLIEnv(t)
	emptyBin := t.TempDir()
	workspaceID := seedCheckWorkspace(t, e, "aws", "some-secret-looking-profile-name")
	e.runWithPath(t, emptyBin, "check", "--provider", "aws", "--workspace", workspaceID, "--profile", "some-secret-looking-profile-name")

	events, err := audit.List(filepath.Join(e.configDir, "audit.log"), 0)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Action != "check" {
		t.Fatalf("action = %q, want check", ev.Action)
	}
	if len(ev.Keys) != 0 {
		t.Fatalf("keys = %+v, want none — check has no field-level keys to report", ev.Keys)
	}
	// The raw audit line itself must never contain "AWS CLI" (the vendor-CLI
	// error text) — belt-and-suspenders beyond checking the typed fields.
	raw, err := os.ReadFile(filepath.Join(e.configDir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "not found in PATH") {
		t.Fatalf("audit.log leaked vendor-CLI error text: %s", raw)
	}
}

// TestCheckTimeoutAbortsWallClockTimePromptly pins provider.Checker's own
// contract (see its doc comment: "scoped by ctx so a hung vendor CLI can be
// aborted") at the CLI level. A fake "aws" that is a two-line shell script
// (an extremely ordinary shape for a real-world credential-helper/SSO
// wrapper around aws/gcloud) sleeps far longer than --timeout. If the
// process is genuinely aborted, `ezcloud check` must return in roughly
// --timeout seconds, not wait for the fake binary's full sleep duration.
func TestCheckTimeoutAbortsWallClockTimePromptly(t *testing.T) {
	e := newCLIEnv(t)
	fakeBinDir := t.TempDir()
	script := filepath.Join(fakeBinDir, "aws")
	// /bin/sleep by absolute path, deliberately NOT bare "sleep" — PATH is
	// about to be pinned to fakeBinDir alone (see runWithPath), which would
	// make a bare "sleep" call fail instantly with "command not found",
	// masking the very hang this test needs to create.
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 6\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	workspaceID := seedCheckWorkspace(t, e, "aws", "whatever")
	stdout, stderr, code := e.runWithPath(t, fakeBinDir, "check", "--provider", "aws", "--workspace", workspaceID, "--profile", "whatever", "--timeout", "1")
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("check exit %d, stderr: %s", code, stderr)
	}

	var result provider.CheckResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout)
	}
	if result.OK {
		t.Fatal("expected ok=false for a timed-out check")
	}

	// Generous slack over the 1s deadline, but nowhere near the fake
	// binary's 6s sleep — if this fails, --timeout only changes the
	// eventual error MESSAGE, it does not actually bound wall-clock time.
	if elapsed > 4*time.Second {
		t.Fatalf("check took %s to return with --timeout=1 (fake vendor CLI sleeps 6s) — "+
			"the vendor-CLI process was not actually aborted at the deadline, only detected "+
			"as late after it eventually exited on its own; result: %+v", elapsed, result)
	}
}
