package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConnectionsAuthSIGTERMCancelsVendorGroupAndCleansScratch(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	gcloudPath := filepath.Join(binDir, "gcloud")
	script := `#!/bin/sh
case "$1:$2:$3" in
  config:configurations:create)
    exit 0
    ;;
  auth:login:*)
    echo $$ > "$TMPDIR/vendor.pid"
    : > "$TMPDIR/vendor.started"
    echo 'https://device.example.test CODE-SECRET-123'
    echo 'TOKEN-SENTINEL' >&2
    while true; do /bin/sleep 10; done
    ;;
  config:configurations:delete)
    : > "$TMPDIR/cleanup.done"
    exit 0
    ;;
esac
exit 4
`
	if err := os.WriteFile(gcloudPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	gcpRoot := filepath.Join(stateDir, "gcloud")
	if err := os.MkdirAll(gcpRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(gcpRoot, "active_config")
	adcPath := filepath.Join(gcpRoot, "application_default_credentials.json")
	if err := os.WriteFile(activePath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adcPath, []byte(`{"sentinel":"unchanged"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	e := newCLIEnv(t)
	cmd := exec.Command(ezcloudBinary, "connections", "auth", "login", "--provider", "gcp")
	cmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
		"HOME="+stateDir,
		"TMPDIR="+stateDir,
		"PATH="+binDir,
		"CLOUDSDK_CONFIG="+gcpRoot,
		"AWS_CONFIG_FILE="+filepath.Join(stateDir, "aws-config"),
		"AWS_SHARED_CREDENTIALS_FILE="+filepath.Join(stateDir, "aws-credentials"),
	)
	cmd.Stdin = strings.NewReader(`{}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	startedPath := filepath.Join(stateDir, "vendor.started")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(startedPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("fake gcloud login never started; stderr=%s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("canceled auth command unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("auth command did not exit after SIGTERM")
	}

	if _, err := os.Stat(filepath.Join(stateDir, "cleanup.done")); err != nil {
		t.Fatalf("scratch cleanup did not run: %v; stderr=%s", err, stderr.String())
	}
	pidData, err := os.ReadFile(filepath.Join(stateDir, "vendor.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err == nil || err != syscall.ESRCH {
		t.Fatalf("vendor process %d still exists after core cancellation: %v", pid, err)
	}
	for _, forbidden := range []string{"device.example.test", "CODE-SECRET-123", "TOKEN-SENTINEL"} {
		if strings.Contains(stdout.String(), forbidden) || strings.Contains(stderr.String(), forbidden) {
			t.Fatalf("vendor auth material %q leaked; stdout=%q stderr=%q", forbidden, stdout.String(), stderr.String())
		}
	}
	if !strings.Contains(stderr.String(), "connection sign-in operation canceled") {
		t.Fatalf("cancellation was not sanitized: %q", stderr.String())
	}
	for path, want := range map[string]string{
		activePath: "existing",
		adcPath:    `{"sentinel":"unchanged"}`,
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s changed: got %q err=%v, want %q", filepath.Base(path), data, err, want)
		}
	}
}
