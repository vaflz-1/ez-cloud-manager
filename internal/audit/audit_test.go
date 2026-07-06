package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpLog(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "audit.log")
}

func TestAppendListRoundtrip(t *testing.T) {
	path := tmpLog(t)
	if err := Append(path, Event{
		Action:   "save",
		Provider: "aws",
		Profile:  "default",
		Keys:     []string{"aws_secret_access_key", "aws_access_key_id"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := Append(path, Event{Action: "delete", Provider: "aws", Profile: "old"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	events, err := List(path, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	// Newest LAST (chronological).
	if events[0].Action != "save" || events[1].Action != "delete" {
		t.Fatalf("wrong order: %+v", events)
	}
	// Time auto-filled.
	if events[0].Time == "" {
		t.Fatal("time not filled")
	}
	// Keys sorted + deduped.
	if got := strings.Join(events[0].Keys, ","); got != "aws_access_key_id,aws_secret_access_key" {
		t.Fatalf("keys = %q", got)
	}
}

func TestAppendWritesRestrictivePerms(t *testing.T) {
	path := tmpLog(t)
	if err := Append(path, Event{Action: "x"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestAppendDedupesKeys(t *testing.T) {
	path := tmpLog(t)
	if err := Append(path, Event{Action: "save", Keys: []string{"b", "a", "b", "a"}}); err != nil {
		t.Fatal(err)
	}
	events, _ := List(path, 0)
	if got := strings.Join(events[0].Keys, ","); got != "a,b" {
		t.Fatalf("keys = %q, want a,b", got)
	}
}

func TestListLimit(t *testing.T) {
	path := tmpLog(t)
	for i := 0; i < 5; i++ {
		if err := Append(path, Event{Action: "a", Detail: string(rune('0' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := List(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2, got %d", len(events))
	}
	// Last two, still chronological: details "3" then "4".
	if events[0].Detail != "3" || events[1].Detail != "4" {
		t.Fatalf("limit returned the wrong window: %+v", events)
	}
}

func TestListSkipsMalformedLines(t *testing.T) {
	path := tmpLog(t)
	if err := Append(path, Event{Action: "good1"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("this is not json\n{ broken\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, Event{Action: "good2"}); err != nil {
		t.Fatal(err)
	}

	events, err := List(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("malformed lines not skipped, got %d events", len(events))
	}
	if events[0].Action != "good1" || events[1].Action != "good2" {
		t.Fatalf("events = %+v", events)
	}
}

func TestListMissingFile(t *testing.T) {
	events, err := List(filepath.Join(t.TempDir(), "nope.log"), 0)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want empty, got %d", len(events))
	}
}

func TestChangedKeys(t *testing.T) {
	oldF := map[string]string{"a": "1", "b": "2", "c": "3"}
	newF := map[string]string{"a": "1", "b": "changed", "d": "4"}
	// a unchanged, b changed, c removed, d added.
	got := ChangedKeys(oldF, newF)
	if want := "b,c,d"; strings.Join(got, ",") != want {
		t.Fatalf("ChangedKeys = %v, want %v", got, want)
	}
}

func TestChangedKeysEdgeCases(t *testing.T) {
	if got := ChangedKeys(nil, nil); len(got) != 0 {
		t.Fatalf("nil maps: want empty, got %v", got)
	}
	if got := ChangedKeys(map[string]string{}, map[string]string{"x": "1"}); strings.Join(got, ",") != "x" {
		t.Fatalf("add-only: got %v", got)
	}
	if got := ChangedKeys(map[string]string{"x": "1"}, map[string]string{}); strings.Join(got, ",") != "x" {
		t.Fatalf("remove-only: got %v", got)
	}
}

func TestChangedKeysReturnsNamesNotValues(t *testing.T) {
	oldF := map[string]string{"aws_secret_access_key": "OLDSECRET"}
	newF := map[string]string{"aws_secret_access_key": "NEWSECRET"}
	got := ChangedKeys(oldF, newF)
	if len(got) != 1 || got[0] != "aws_secret_access_key" {
		t.Fatalf("got %v", got)
	}
	for _, k := range got {
		if strings.Contains(k, "SECRET") {
			t.Fatalf("ChangedKeys leaked a value: %q", k)
		}
	}
}

func TestRotationCreatesDotOne(t *testing.T) {
	path := tmpLog(t)
	writeOversizedLog(t, path, "x")

	// The next append sees an oversized log and rotates it to .1 first.
	if err := Append(path, Event{Action: "after-rotate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file %s.1: %v", path, err)
	}

	events, err := List(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "after-rotate" {
		t.Fatalf("post-rotation log wrong: %+v", events)
	}
}

func TestRotationReplacesPreviousDotOne(t *testing.T) {
	path := tmpLog(t)
	if err := os.WriteFile(path+".1", []byte("old rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOversizedLog(t, path, "y")

	if err := Append(path, Event{Action: "z"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old rotation") {
		t.Fatal("previous .1 was not replaced")
	}
}

// TestAppendDoesNotLogSecretValues is the security guard for the package
// invariant: a save whose field maps contain secret VALUES must produce an
// audit line that records only the key NAMES.
func TestAppendDoesNotLogSecretValues(t *testing.T) {
	path := tmpLog(t)
	const secret = "wJalrXUtnFEMI-SUPERSECRET-KEY"
	oldFields := map[string]string{"aws_access_key_id": "AKIAOLD"}
	newFields := map[string]string{
		"aws_access_key_id":     "AKIANEW",
		"aws_secret_access_key": secret,
	}

	// A caller records only key NAMES via ChangedKeys — never the values.
	if err := Append(path, Event{
		Action:   "save",
		Provider: "aws",
		Profile:  "default",
		Keys:     ChangedKeys(oldFields, newFields),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if strings.Contains(line, secret) {
		t.Fatalf("audit log leaked the secret value: %s", line)
	}
	if strings.Contains(line, "AKIANEW") || strings.Contains(line, "AKIAOLD") {
		t.Fatalf("audit log leaked a field value: %s", line)
	}
	// It SHOULD still record the key names.
	if !strings.Contains(line, "aws_secret_access_key") || !strings.Contains(line, "aws_access_key_id") {
		t.Fatalf("audit log should record key names: %s", line)
	}
}

// writeOversizedLog writes more than maxLogBytes of newline-terminated data so
// the next Append triggers rotation.
func writeOversizedLog(t *testing.T, path, fill string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	chunk := strings.Repeat(fill, 1024) + "\n"
	written := 0
	for written <= maxLogBytes {
		n, err := f.WriteString(chunk)
		if err != nil {
			t.Fatal(err)
		}
		written += n
	}
}
