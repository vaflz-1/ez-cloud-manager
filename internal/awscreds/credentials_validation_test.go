package awscreds

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveRejectsUnsafeKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	// Each of these would break the INI layout or be silently swallowed as a
	// comment (# / ;) on the next read.
	for _, key := range []string{"#comment", ";semi", "foo=bar", "foo]", "[foo", ".hidden", "-lead", "foo!bar", "foo bar!"} {
		if err := Save(path, "demo", map[string]string{key: "v"}); err == nil {
			t.Fatalf("expected rejection for key %q", key)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("no file should be written when a key is rejected")
	}
}

func TestSaveAcceptsReasonableKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	fields := map[string]string{
		"aws_access_key_id": "AKIA",
		"custom-key.v2":     "ok",
		"x_9":               "ok",
	}
	if err := Save(path, "demo", fields); err != nil {
		t.Fatalf("valid keys were rejected: %v", err)
	}
}

func TestSaveRejectsControlCharValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	withBell := "us-" + string(rune(7)) + "east" // embed a bell (0x07)
	if err := Save(path, "demo", map[string]string{"region": withBell}); err == nil {
		t.Fatal("expected rejection for a control character in a value")
	}
	if err := Save(path, "demo", map[string]string{"region": "a\tb"}); err != nil {
		t.Fatalf("a tab in a value should be allowed: %v", err)
	}
}
