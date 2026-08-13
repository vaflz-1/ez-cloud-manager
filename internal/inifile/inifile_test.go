package inifile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLongPhysicalLineDoesNotTruncateFollowingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	longValue := strings.Repeat("x", 128<<10)
	original := "[core]\nlarge = " + longValue + "\nsentinel = keep-me\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	model, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(model.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(model.Sections))
	}
	fields := model.Sections[0].Fields()
	if fields["large"] != longValue {
		t.Fatalf("long value length = %d, want %d", len(fields["large"]), len(longValue))
	}
	if fields["sentinel"] != "keep-me" {
		t.Fatalf("content after long line was lost: %+v", fields)
	}

	model.Sections[0].ApplyFields(map[string]string{"sentinel": "still-here"})
	if err := WriteAtomic(path, model, false); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rewritten)
	if !strings.Contains(text, "large = "+longValue+"\n") {
		t.Fatal("rewrite truncated the long line")
	}
	if !strings.Contains(text, "sentinel = still-here\n") {
		t.Fatal("rewrite lost content following the long line")
	}
}

func TestReadLimitedRejectsOversizedSnapshotWithoutPartialModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("[core]\nvalue = "+strings.Repeat("x", 128)+"\nsentinel = keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLimited(path, 64); err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("oversized file was accepted: %v", err)
	}
	model, err := ReadLimited(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Sections[0].Fields()["sentinel"]; got != "keep" {
		t.Fatalf("bounded snapshot was parsed partially: %q", got)
	}
}
