package awscreds

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ez-cloud-manager/internal/pathlock"
)

func TestConcurrentSavesSerializeWithoutLostProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
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
			done <- Save(path, name, map[string]string{KeyRegion: "eu-west-1"})
		}()
	}
	<-attempted
	<-attempted
	assertNoAWSMutationCompleted(t, done)

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

func TestConditionalSaveAllowsExactlyOneConcurrentUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	baseline := map[string]string{KeyRegion: "eu-west-1"}
	if err := Save(path, "shared", baseline); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	done := make(chan error, 2)
	for _, region := range []string{"us-east-1", "ap-southeast-1"} {
		region := region
		go func() {
			<-start
			done <- SaveIfUnchanged(path, "shared", map[string]string{KeyRegion: region}, baseline, false)
		}()
	}
	close(start)

	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-done; {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("conditional save returned unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want exactly one of each", successes, conflicts)
	}
	if err := SaveIfUnchanged(path, "new", baseline, nil, true); err != nil {
		t.Fatalf("create with absent precondition: %v", err)
	}
	if err := SaveIfUnchanged(path, "new", baseline, nil, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("second absent-precondition create error = %v, want ErrConflict", err)
	}
}

func assertNoAWSMutationCompleted(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("mutation ignored the held credential lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}
