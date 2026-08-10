package azurecreds

import (
	"path/filepath"
	"testing"
	"time"

	"ez-cloud-manager/internal/pathlock"
)

func TestConcurrentSavesSerializeWithoutLostProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure_profiles.ini")
	release, err := pathlock.Acquire(path)
	if err != nil {
		t.Fatalf("hold credential lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	attempted := make(chan struct{}, 2)
	done := make(chan error, 2)
	for _, name := range []string{"alpha", "beta"} {
		name := name
		go func() {
			attempted <- struct{}{}
			done <- Save(path, name, map[string]string{KeyTenantID: name + "-tenant"})
		}()
	}
	<-attempted
	<-attempted
	assertNoAzureMutationCompleted(t, done)

	if err := release(); err != nil {
		t.Fatalf("release credential lock: %v", err)
	}
	released = true
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}

	profiles, err := List(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("concurrent saves lost a profile: %+v", profiles)
	}
}

func assertNoAzureMutationCompleted(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("mutation ignored the held credential lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}
