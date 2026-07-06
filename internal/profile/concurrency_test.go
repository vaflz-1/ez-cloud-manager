package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// These tests target the concurrency guarantee the package actually makes:
// writeProfile's temp-file-then-rename discipline means any single profile.json
// is never observed torn or partially written, even when hammered from many
// goroutines at once. They do NOT assert cross-call invariants like "no two
// concurrent Creates can ever pick the same name" — there is no cross-process
// lock (same as internal/workspace before it), so that race is a known,
// pre-existing architectural property, not something these tests pretend
// isn't there.

// TestConcurrentCreateDistinctNamesIsRaceFree creates N profiles with unique
// names from concurrent goroutines (run this file with -race) and checks
// every one lands intact: no lost writes, no corrupted JSON, no duplicate or
// missing IDs.
func TestConcurrentCreateDistinctNamesIsRaceFree(t *testing.T) {
	root := tmpRoot(t)
	const n = 20

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Create(root, Profile{Name: fmt.Sprintf("profile-%02d", i)})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != n {
		t.Fatalf("want %d profiles, got %d", n, len(list))
	}
	seenIDs := make(map[string]bool, n)
	for _, p := range list {
		if seenIDs[p.ID] {
			t.Fatalf("duplicate id %q in list", p.ID)
		}
		seenIDs[p.ID] = true
	}
}

// TestConcurrentSaveSameProfileNeverTornWrites repeatedly saves the SAME
// profile from many goroutines with distinct payloads. The atomic
// temp+rename write means the file on disk after everything settles must
// always be one of the fully-written payloads — valid JSON with a
// consistent id/name pairing — never a half-written mix of two writes.
func TestConcurrentSaveSameProfileNeverTornWrites(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{Name: "shared"})

	const n = 30
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := created
			p.EnvVars = []EnvVar{{Key: "ITER", Value: fmt.Sprintf("%d", i)}}
			errs[i] = Save(root, p)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	// Read the raw bytes directly (bypassing readProfile's own validation)
	// to confirm the file is well-formed JSON, not a torn write.
	data, err := os.ReadFile(filepath.Join(root, created.ID, "profile.json"))
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	var final Profile
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("final profile.json is not valid JSON (torn write): %v\ncontent: %s", err, data)
	}
	if final.ID != created.ID {
		t.Fatalf("final id = %q, want %q", final.ID, created.ID)
	}
	if len(final.EnvVars) != 1 || final.EnvVars[0].Key != "ITER" {
		t.Fatalf("final env vars malformed (torn write): %+v", final.EnvVars)
	}
}

// TestConcurrentListDuringWritesIsRaceFree exercises readers and writers on
// the same root at once (List never mutates, but it does read directory
// entries that Create/Delete are simultaneously changing). It only needs
// -race and "does not panic/error unexpectedly" to prove the point; List
// tolerates a directory entry disappearing mid-scan the same way it
// tolerates a corrupt one (see TestListSkipsCorruptFolder).
func TestConcurrentListDuringWritesIsRaceFree(t *testing.T) {
	root := tmpRoot(t)
	mustCreate(t, root, Profile{Name: "seed"})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = Create(root, Profile{Name: fmt.Sprintf("writer-%d", i)})
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := List(root); err != nil {
				t.Errorf("list during concurrent writes: %v", err)
			}
		}()
	}
	wg.Wait()

	list, err := List(root)
	if err != nil {
		t.Fatalf("final list: %v", err)
	}
	if len(list) != 11 {
		t.Fatalf("want 11 profiles (1 seed + 10 writers), got %d", len(list))
	}
}
