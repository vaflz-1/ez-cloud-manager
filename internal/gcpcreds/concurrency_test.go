package gcpcreds

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ez-cloud-manager/internal/pathlock"
)

func TestConcurrentSavesSerializeWithoutLostProperties(t *testing.T) {
	root := t.TempDir()
	path := configFile(root, "shared")
	release, err := pathlock.Acquire(path)
	if err != nil {
		t.Fatalf("hold configuration lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	attempted := make(chan struct{}, 2)
	done := make(chan error, 2)
	for _, fields := range []map[string]string{
		{KeyProject: "project-a"},
		{KeyRegion: "europe-west1"},
	} {
		fields := fields
		go func() {
			attempted <- struct{}{}
			done <- Save(root, "shared", fields)
		}()
	}
	<-attempted
	<-attempted
	assertNoGCPMutationCompleted(t, done)

	if err := release(); err != nil {
		t.Fatalf("release configuration lock: %v", err)
	}
	released = true
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}

	profile, err := Get(root, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Fields[KeyProject] != "project-a" || profile.Fields[KeyRegion] != "europe-west1" {
		t.Fatalf("concurrent saves lost a property: %+v", profile.Fields)
	}
}

func TestActivateAndDeleteShareTheActiveStateLock(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, "one", map[string]string{KeyProject: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, "two", map[string]string{KeyProject: "p2"}); err != nil {
		t.Fatal(err)
	}
	if err := Activate(root, "one"); err != nil {
		t.Fatal(err)
	}

	t.Run("activate", func(t *testing.T) {
		runWhileGCPPathLocked(t, activeConfigFile(root), func() error {
			return Activate(root, "two")
		})
		if got := ActiveName(root); got != "two" {
			t.Fatalf("active configuration = %q, want two", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		runWhileGCPPathLocked(t, activeConfigFile(root), func() error {
			return Delete(root, "one")
		})
		configPath := configFile(root, "one")
		if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted configuration still exists: %v", err)
		}
		backups, err := filepath.Glob(configPath + ".bak.*")
		if err != nil {
			t.Fatal(err)
		}
		if len(backups) == 0 {
			t.Fatal("delete did not preserve a backup")
		}
	})
}

func runWhileGCPPathLocked(t *testing.T, path string, mutation func() error) {
	t.Helper()
	release, err := pathlock.Acquire(path)
	if err != nil {
		t.Fatalf("hold path lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	attempted := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		attempted <- struct{}{}
		done <- mutation()
	}()
	<-attempted
	assertNoGCPMutationCompleted(t, done)
	if err := release(); err != nil {
		t.Fatalf("release path lock: %v", err)
	}
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mutation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for mutation")
	}
}

func assertNoGCPMutationCompleted(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("mutation ignored the held storage lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}
