package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "workspaces.json")
}

func mustSave(t *testing.T, path string, w Workspace) {
	t.Helper()
	if err := Save(path, w); err != nil {
		t.Fatalf("save %q: %v", w.Name, err)
	}
}

func TestSaveListRoundtrip(t *testing.T) {
	path := tmpFile(t)
	w := Workspace{Name: "prod", Members: []Member{{Provider: "aws", Profile: "default"}}}
	mustSave(t, path, w)

	list, err := List(path)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(list))
	}
	if list[0].Name != "prod" {
		t.Fatalf("name = %q", list[0].Name)
	}
	if len(list[0].Members) != 1 || list[0].Members[0] != (Member{Provider: "aws", Profile: "default"}) {
		t.Fatalf("members = %+v", list[0].Members)
	}
}

func TestSaveWritesRestrictivePerms(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestSaveWritesVersion(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != 1 {
		t.Fatalf("version = %d, want 1", envelope.Version)
	}
}

func TestSaveUpsertsByName(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "prod", Members: []Member{{Provider: "aws", Profile: "old"}}})
	mustSave(t, path, Workspace{Name: "prod", Members: []Member{{Provider: "aws", Profile: "new"}}})

	list, err := List(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("upsert should keep one entry, got %d", len(list))
	}
	if list[0].Members[0].Profile != "new" {
		t.Fatalf("profile = %q, want new", list[0].Members[0].Profile)
	}
}

func TestSaveUpsertIsCaseSensitive(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "Prod", Members: []Member{{Provider: "aws", Profile: "p"}}})
	mustSave(t, path, Workspace{Name: "prod", Members: []Member{{Provider: "aws", Profile: "p"}}})

	list, err := List(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("case-sensitive names should be distinct, got %d", len(list))
	}
}

func TestListSortedByName(t *testing.T) {
	path := tmpFile(t)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		mustSave(t, path, Workspace{Name: n, Members: []Member{{Provider: "aws", Profile: "p"}}})
	}

	list, err := List(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(list))
	for _, w := range list {
		got = append(got, w.Name)
	}
	if want := "alpha,mid,zeta"; strings.Join(got, ",") != want {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDelete(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})
	mustSave(t, path, Workspace{Name: "b", Members: []Member{{Provider: "aws", Profile: "p"}}})

	if err := Delete(path, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ := List(path)
	if len(list) != 1 || list[0].Name != "b" {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestDeleteAbsentIsNoOp(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})

	if err := Delete(path, "missing"); err != nil {
		t.Fatalf("delete absent: %v", err)
	}
	list, _ := List(path)
	if len(list) != 1 {
		t.Fatalf("no-op delete should leave list intact, got %d", len(list))
	}
}

func TestRename(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "old", Members: []Member{{Provider: "aws", Profile: "p"}}})

	if err := Rename(path, "old", "new"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	list, _ := List(path)
	if len(list) != 1 || list[0].Name != "new" {
		t.Fatalf("after rename: %+v", list)
	}
}

func TestRenameToExistingFails(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})
	mustSave(t, path, Workspace{Name: "b", Members: []Member{{Provider: "aws", Profile: "p"}}})

	if err := Rename(path, "a", "b"); err == nil {
		t.Fatal("expected error renaming onto an existing name")
	}
}

func TestRenameMissingFails(t *testing.T) {
	path := tmpFile(t)
	if err := Rename(path, "ghost", "new"); err == nil {
		t.Fatal("expected error renaming a missing workspace")
	}
}

func TestRenameToSameNameIsAllowed(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})

	if err := Rename(path, "a", "a"); err != nil {
		t.Fatalf("rename to same name should be a no-op, got %v", err)
	}
	list, _ := List(path)
	if len(list) != 1 || list[0].Name != "a" {
		t.Fatalf("after self-rename: %+v", list)
	}
}

func TestRenameRejectsInvalidNewName(t *testing.T) {
	path := tmpFile(t)
	mustSave(t, path, Workspace{Name: "a", Members: []Member{{Provider: "aws", Profile: "p"}}})

	if err := Rename(path, "a", "  "); err == nil {
		t.Fatal("expected error renaming to an empty name")
	}
}

func TestListMissingFile(t *testing.T) {
	list, err := List(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestListEmptyFile(t *testing.T) {
	path := tmpFile(t)
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := List(path)
	if err != nil {
		t.Fatalf("empty file should not error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestListRejectsNewerVersion(t *testing.T) {
	path := tmpFile(t)
	if err := os.WriteFile(path, []byte(`{"version":2,"workspaces":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := List(path)
	if err == nil {
		t.Fatal("expected error for newer schema version")
	}
	if !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("error = %v, want mention of a newer version", err)
	}
}

func TestSaveValidationRejections(t *testing.T) {
	path := tmpFile(t)
	longName := strings.Repeat("x", maxNameLen+1)
	cases := []struct {
		desc string
		w    Workspace
	}{
		{"empty name", Workspace{Name: "  ", Members: []Member{{Provider: "aws", Profile: "p"}}}},
		{"too long name", Workspace{Name: longName, Members: []Member{{Provider: "aws", Profile: "p"}}}},
		{"control char name", Workspace{Name: "bad\nname", Members: []Member{{Provider: "aws", Profile: "p"}}}},
		{"empty provider", Workspace{Name: "ok", Members: []Member{{Provider: " ", Profile: "p"}}}},
		{"empty profile", Workspace{Name: "ok", Members: []Member{{Provider: "aws", Profile: ""}}}},
		{"control char member", Workspace{Name: "ok", Members: []Member{{Provider: "aws", Profile: "p\x00"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			if err := Save(path, tc.w); err == nil {
				t.Fatalf("expected validation error for %s", tc.desc)
			}
		})
	}
}

func TestSaveNameLengthBoundary(t *testing.T) {
	path := tmpFile(t)
	name := strings.Repeat("y", maxNameLen)
	if err := Save(path, Workspace{Name: name, Members: []Member{{Provider: "aws", Profile: "p"}}}); err != nil {
		t.Fatalf("a %d-char name should be allowed: %v", maxNameLen, err)
	}
}

func TestSaveDedupesMembers(t *testing.T) {
	path := tmpFile(t)
	w := Workspace{Name: "a", Members: []Member{
		{Provider: "aws", Profile: "p"},
		{Provider: "aws", Profile: "p"},
		{Provider: "gcp", Profile: "q"},
	}}
	mustSave(t, path, w)

	list, _ := List(path)
	if len(list[0].Members) != 2 {
		t.Fatalf("members not deduped: %+v", list[0].Members)
	}
}

func TestSaveTrimsAndDedupesMembers(t *testing.T) {
	path := tmpFile(t)
	w := Workspace{Name: "a", Members: []Member{
		{Provider: " aws ", Profile: " p "},
		{Provider: "aws", Profile: "p"},
	}}
	mustSave(t, path, w)

	list, _ := List(path)
	if len(list[0].Members) != 1 {
		t.Fatalf("trim+dedupe failed: %+v", list[0].Members)
	}
	if list[0].Members[0] != (Member{Provider: "aws", Profile: "p"}) {
		t.Fatalf("member not trimmed: %+v", list[0].Members[0])
	}
}
