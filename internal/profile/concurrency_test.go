package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ez-cloud-manager/internal/plugin"
)

// These tests target both concurrency layers: writeProfile's temp-file rename
// prevents torn JSON, existing-profile mutations use a stable per-profile
// advisory lock, and root-wide name/cardinality invariants use a second stable
// advisory lock acquired before any per-profile lock.

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

// TestConcurrentTargetedMutationsSerializeAndPreserveIndependentFields holds
// the profile lock before starting all three targeted writers. Each writer
// must block until release; afterwards their order may vary, but all three
// independent patches must survive in the single final profile.
func TestConcurrentTargetedMutationsSerializeAndPreserveIndependentFields(t *testing.T) {
	root := tmpRoot(t)
	created := mustCreate(t, root, Profile{
		Name:           "before",
		EnabledPlugins: []string{"some-future-plugin"},
		WindowState:    json.RawMessage(`{"selected":"catalog"}`),
	})

	release, err := acquireProfileLock(root, created.ID)
	if err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	type result struct {
		writer string
		err    error
	}
	start := make(chan struct{})
	attempted := make(chan string, 3)
	done := make(chan result, 3)
	launch := func(name string, write func() error) {
		go func() {
			<-start
			attempted <- name
			done <- result{writer: name, err: write()}
		}()
	}
	launch("core", func() error {
		_, err := UpdateCore(root, CoreUpdate{
			ID:              created.ID,
			Name:            "after",
			EnvVars:         []EnvVar{{Key: "REGION", Value: "eu-west-1"}},
			ExpectedName:    created.Name,
			ExpectedEnvVars: created.EnvVars,
		})
		return err
	})
	launch("plugins", func() error {
		_, err := UpdateEnabledPlugins(root, created.ID, map[string]bool{plugin.CloudAccountsID: true})
		return err
	})
	launch("settings", func() error {
		_, err := SetSettingsBlob(root, created.ID, "some-future-plugin", json.RawMessage(`{"mode":"safe"}`))
		return err
	})
	close(start)
	for range 3 {
		<-attempted
	}

	select {
	case early := <-done:
		t.Fatalf("targeted writer %q ignored the held profile lock: %v", early.writer, early.err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := release(); err != nil {
		t.Fatalf("release test lock: %v", err)
	}
	released = true

	for range 3 {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("%s mutation: %v", result.writer, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for targeted mutation")
		}
	}

	final, err := Get(root, created.ID)
	if err != nil {
		t.Fatalf("get final profile: %v", err)
	}
	if final.Name != "after" || len(final.EnvVars) != 1 || final.EnvVars[0] != (EnvVar{Key: "REGION", Value: "eu-west-1"}) {
		t.Fatalf("core mutation was lost: name %q, envVars %+v", final.Name, final.EnvVars)
	}
	if len(final.EnabledPlugins) != 2 || final.EnabledPlugins[0] != "some-future-plugin" || final.EnabledPlugins[1] != plugin.CloudAccountsID {
		t.Fatalf("plugin mutation was lost: %+v", final.EnabledPlugins)
	}
	var settings struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(final.Settings["some-future-plugin"], &settings); err != nil || settings.Mode != "safe" {
		t.Fatalf("settings mutation was lost: %s (%v)", final.Settings["some-future-plugin"], err)
	}
	var state struct {
		Selected string `json:"selected"`
	}
	if err := json.Unmarshal(final.WindowState, &state); err != nil || state.Selected != "catalog" {
		t.Fatalf("window state was clobbered: %s (%v)", final.WindowState, err)
	}
}

// TestConcurrentNameOperationsPreserveUniqueNames holds the root lock until
// both contenders have entered their public operation. That makes the race
// deterministic: both must wait, then observe one another's committed result
// in root-lock order instead of validating the same stale directory snapshot.
func TestConcurrentNameOperationsPreserveUniqueNames(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		root := tmpRoot(t)
		errs := runWhileRootLocked(t, root,
			func() error { _, err := Create(root, Profile{Name: "shared"}); return err },
			func() error { _, err := Create(root, Profile{Name: "shared"}); return err },
		)
		assertSuccessCount(t, errs, 1)
		assertUniqueProfileNames(t, root, 1)
	})

	t.Run("duplicate", func(t *testing.T) {
		root := tmpRoot(t)
		source := mustCreate(t, root, Profile{Name: "source"})
		errs := runWhileRootLocked(t, root,
			func() error { _, err := Duplicate(root, source.ID, "shared copy"); return err },
			func() error { _, err := Duplicate(root, source.ID, "shared copy"); return err },
		)
		assertSuccessCount(t, errs, 1)
		assertUniqueProfileNames(t, root, 2)
	})

	t.Run("rename", func(t *testing.T) {
		root := tmpRoot(t)
		a := mustCreate(t, root, Profile{Name: "a"})
		b := mustCreate(t, root, Profile{Name: "b"})
		errs := runWhileRootLocked(t, root,
			func() error { return Rename(root, a.ID, "shared") },
			func() error { return Rename(root, b.ID, "shared") },
		)
		assertSuccessCount(t, errs, 1)
		assertUniqueProfileNames(t, root, 2)
	})

	t.Run("import", func(t *testing.T) {
		root := tmpRoot(t)
		var archive bytes.Buffer
		if err := writeZip(&archive, map[string]string{
			"profile.json": `{"name":"shared import","envVars":[]}`,
		}); err != nil {
			t.Fatal(err)
		}
		data := archive.Bytes()
		errs := runWhileRootLocked(t, root,
			func() error { _, err := Import(root, bytes.NewReader(data)); return err },
			func() error { _, err := Import(root, bytes.NewReader(data)); return err },
		)
		assertSuccessCount(t, errs, 2)
		assertUniqueProfileNames(t, root, 2)
	})
}

// TestConcurrentDeleteCannotRemoveEveryProfile forces two deletes of distinct
// IDs to contend on the root invariant. Exactly one may commit; the second
// must re-list after it and refuse to remove the last remaining profile.
func TestConcurrentDeleteCannotRemoveEveryProfile(t *testing.T) {
	root := tmpRoot(t)
	a := mustCreate(t, root, Profile{Name: "a"})
	b := mustCreate(t, root, Profile{Name: "b"})

	errs := runWhileRootLocked(t, root,
		func() error { return Delete(root, a.ID) },
		func() error { return Delete(root, b.ID) },
	)
	assertSuccessCount(t, errs, 1)

	failures := 0
	for _, err := range errs {
		if err == nil {
			continue
		}
		failures++
		if !strings.Contains(err.Error(), "cannot delete the last remaining profile") {
			t.Fatalf("unexpected delete error: %v", err)
		}
	}
	if failures != 1 {
		t.Fatalf("delete failures = %d, want 1: %v", failures, errs)
	}
	assertUniqueProfileNames(t, root, 1)
}

// runWhileRootLocked starts every operation behind a root lock owned by the
// test, proves none can finish before that lock is released, then returns all
// outcomes. The held lock turns timing-sensitive stale-snapshot races into a
// deterministic regression harness.
func runWhileRootLocked(t *testing.T, root string, operations ...func() error) []error {
	t.Helper()
	release, err := acquireRootLock(root)
	if err != nil {
		t.Fatalf("acquire root test lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = release()
		}
	}()

	start := make(chan struct{})
	attempted := make(chan struct{}, len(operations))
	done := make(chan error, len(operations))
	for _, operation := range operations {
		operation := operation
		go func() {
			<-start
			attempted <- struct{}{}
			done <- operation()
		}()
	}
	close(start)
	for range operations {
		<-attempted
	}

	select {
	case early := <-done:
		t.Fatalf("root-invariant operation ignored the held root lock: %v", early)
	case <-time.After(250 * time.Millisecond):
	}
	if err := release(); err != nil {
		t.Fatalf("release root test lock: %v", err)
	}
	released = true

	results := make([]error, 0, len(operations))
	for range operations {
		select {
		case result := <-done:
			results = append(results, result)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for root-invariant operation")
		}
	}
	return results
}

func assertSuccessCount(t *testing.T, errs []error, want int) {
	t.Helper()
	got := 0
	for _, err := range errs {
		if err == nil {
			got++
		}
	}
	if got != want {
		t.Fatalf("successful operations = %d, want %d: %v", got, want, errs)
	}
}

func assertUniqueProfileNames(t *testing.T, root string, wantCount int) {
	t.Helper()
	profiles, err := List(root)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(profiles) != wantCount {
		t.Fatalf("profile count = %d, want %d: %+v", len(profiles), wantCount, profiles)
	}
	seen := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		name := strings.ToLower(p.Name)
		if seen[name] {
			t.Fatalf("duplicate profile name survived: %q", p.Name)
		}
		seen[name] = true
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
