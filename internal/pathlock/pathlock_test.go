package pathlock

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestOrderedLockPathsCanonicalizesSortsAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")

	got, err := orderedLockPaths([]string{b, a, filepath.Join(dir, ".", "a"), b})
	if err != nil {
		t.Fatal(err)
	}
	wantA, err := lockPath(a)
	if err != nil {
		t.Fatal(err)
	}
	wantB, err := lockPath(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{wantA, wantB}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered lock paths = %q, want %q", got, want)
	}
}

func TestAcquireRejectsMissingStoragePath(t *testing.T) {
	if _, err := Acquire(); err == nil {
		t.Fatal("Acquire() should reject an empty path set")
	}
	if _, err := Acquire(""); err == nil {
		t.Fatal("Acquire(\"\") should reject an empty storage path")
	}
}
