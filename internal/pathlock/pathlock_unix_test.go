//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package pathlock

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	helperModeEnv = "EZCLOUD_PATHLOCK_TEST_HELPER"
	helperPathEnv = "EZCLOUD_PATHLOCK_TEST_PATH"
)

func TestAcquireBlocksAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire parent lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	cmd := exec.Command(os.Args[0], "-test.run=^TestPathlockHelperProcess$")
	cmd.Env = append(os.Environ(), helperModeEnv+"=1", helperPathEnv+"="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	waitForHelperLine(t, lines, "PATHLOCK_READY", &stderr)

	select {
	case line := <-lines:
		t.Fatalf("child crossed a lock held by another process: %q", line)
	case <-time.After(250 * time.Millisecond):
	}

	if err := release(); err != nil {
		t.Fatalf("release parent lock: %v", err)
	}
	released = true
	waitForHelperLine(t, lines, "PATHLOCK_ACQUIRED", &stderr)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process: %v: %s", err, stderr.String())
	}
}

func TestAcquireRetainsPrivateStableLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	locks, err := orderedLockPaths([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(locks[0])
	if err != nil {
		t.Fatalf("stat held lock: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("lock permissions = %o, want 600", permissions)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(locks[0]); err != nil {
		t.Fatalf("stable lock inode was removed after release: %v", err)
	}
}

func TestPathlockHelperProcess(t *testing.T) {
	if os.Getenv(helperModeEnv) != "1" {
		t.Skip("subprocess helper")
	}
	path := os.Getenv(helperPathEnv)
	fmt.Println("PATHLOCK_READY")
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("PATHLOCK_ACQUIRED")
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func waitForHelperLine(t *testing.T, lines <-chan string, want string, stderr *bytes.Buffer) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("helper exited before %s: %s", want, stderr.String())
			}
			if strings.TrimSpace(line) == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s: %s", want, stderr.String())
		}
	}
}
