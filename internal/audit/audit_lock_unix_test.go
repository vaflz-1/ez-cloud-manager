//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package audit

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	auditHelperEnv       = "EZCLOUD_AUDIT_TEST_HELPER"
	auditHelperPathEnv   = "EZCLOUD_AUDIT_TEST_PATH"
	auditHelperActionEnv = "EZCLOUD_AUDIT_TEST_ACTION"
	auditHelperReadyEnv  = "EZCLOUD_AUDIT_TEST_READY"
)

func TestAuditLockRepairsExistingPermissions(t *testing.T) {
	path := tmpLog(t)
	lockPath := auditLockPath(path)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := acquireAuditLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("lock permissions = %o, want 600", got)
	}
}

func TestAppendWaitsForCrossProcessLock(t *testing.T) {
	path := tmpLog(t)
	release, err := acquireAuditLock(path)
	if err != nil {
		t.Fatalf("acquire audit lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	ready := filepath.Join(t.TempDir(), "ready")
	command := auditAppendHelperCommand(path, "after-lock", ready)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	waitForAuditHelperReady(t, ready)

	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case err := <-waited:
		t.Fatalf("helper bypassed held cross-process lock: %v (%s)", err, stderr.String())
	case <-time.After(150 * time.Millisecond):
	}

	if err := release(); err != nil {
		t.Fatalf("release audit lock: %v", err)
	}
	released = true
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("helper append: %v (%s)", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not finish after audit lock was released")
	}

	events, err := List(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "after-lock" {
		t.Fatalf("unexpected events after lock release: %+v", events)
	}
}

func TestConcurrentProcessesSerializeRotationAndAppend(t *testing.T) {
	path := tmpLog(t)
	writeOversizedLog(t, path, "rotation-seed")

	release, err := acquireAuditLock(path)
	if err != nil {
		t.Fatalf("acquire audit lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	const count = 12
	type helper struct {
		command *exec.Cmd
		stderr  bytes.Buffer
	}
	helpers := make([]helper, count)
	readyDir := t.TempDir()
	for i := 0; i < count; i++ {
		ready := filepath.Join(readyDir, fmt.Sprintf("ready-%02d", i))
		helpers[i].command = auditAppendHelperCommand(path, fmt.Sprintf("process-%02d", i), ready)
		helpers[i].command.Stderr = &helpers[i].stderr
		if err := helpers[i].command.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
		waitForAuditHelperReady(t, ready)
	}

	if err := release(); err != nil {
		t.Fatalf("release audit lock: %v", err)
	}
	released = true
	for i := range helpers {
		if err := helpers[i].command.Wait(); err != nil {
			t.Fatalf("helper %d append: %v (%s)", i, err, helpers[i].stderr.String())
		}
	}

	rotatedInfo, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("stat rotated log: %v", err)
	}
	if rotatedInfo.Size() <= maxLogBytes {
		t.Fatalf("rotated log size = %d, want > %d", rotatedInfo.Size(), maxLogBytes)
	}

	events, err := List(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != count {
		t.Fatalf("want %d post-rotation events, got %d", count, len(events))
	}
	seen := make(map[string]bool, count)
	for _, event := range events {
		if seen[event.Action] {
			t.Fatalf("duplicate event %q", event.Action)
		}
		seen[event.Action] = true
	}
	for i := 0; i < count; i++ {
		action := fmt.Sprintf("process-%02d", i)
		if !seen[action] {
			t.Fatalf("missing event %q", action)
		}
	}

	lockInfo, err := os.Stat(auditLockPath(path))
	if err != nil {
		t.Fatalf("stat stable lock: %v", err)
	}
	if permissions := lockInfo.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("lock permissions = %o, want 600", permissions)
	}
}

func TestAuditAppendHelperProcess(t *testing.T) {
	if os.Getenv(auditHelperEnv) != "1" {
		return
	}
	path := os.Getenv(auditHelperPathEnv)
	action := os.Getenv(auditHelperActionEnv)
	if ready := os.Getenv(auditHelperReadyEnv); ready != "" {
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "could not signal readiness")
			os.Exit(2)
		}
	}
	if err := Append(path, Event{Action: action}); err != nil {
		fmt.Fprintln(os.Stderr, "audit append failed")
		os.Exit(3)
	}
	os.Exit(0)
}

func auditAppendHelperCommand(path, action, ready string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestAuditAppendHelperProcess$")
	command.Env = append(os.Environ(),
		auditHelperEnv+"=1",
		auditHelperPathEnv+"="+path,
		auditHelperActionEnv+"="+action,
		auditHelperReadyEnv+"="+ready,
	)
	return command
}

func waitForAuditHelperReady(t *testing.T, ready string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat helper readiness: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for audit helper readiness")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
