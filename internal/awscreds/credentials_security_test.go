package awscreds

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSaveRejectsValueInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("[default]\naws_access_key_id = OK\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A value carrying a newline + a fake section header must be rejected,
	// not written — otherwise it injects keys into another profile.
	fields := map[string]string{
		"region": "us-east-1\n[default]\naws_secret_access_key = pwned",
	}
	if err := Save(path, "victim", fields); err == nil {
		t.Fatal("expected Save to reject a newline-injecting value")
	}

	data, _ := os.ReadFile(path)
	text := string(data)
	if strings.Contains(text, "pwned") || strings.Contains(text, "[victim]") {
		t.Fatalf("injection or partial write leaked into file:\n%s", text)
	}
}

func TestSaveRejectsProfileNameInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	for _, bad := range []string{"work]\n[default", "x]", "a\nb", "[", "]"} {
		if err := Save(path, bad, map[string]string{"region": "eu-west-1"}); err == nil {
			t.Fatalf("expected rejection for profile name %q", bad)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("no credentials file should have been created from rejected saves")
	}
}

func TestPruneBackupsRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")

	// 15 fake, age-sortable backups.
	for i := 1; i <= 15; i++ {
		name := fmt.Sprintf("%s.bak.202601%02d-000000", path, i)
		if err := os.WriteFile(name, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("EZCLOUD_MAX_BACKUPS", "10")
	pruneBackups(path)

	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != 10 {
		t.Fatalf("expected 10 backups retained, got %d", len(matches))
	}
	sort.Strings(matches)
	// Oldest (day 01) must be gone; newest (day 15) must survive.
	if strings.Contains(strings.Join(matches, "\n"), "20260101-000000") {
		t.Fatal("oldest backup should have been pruned")
	}
	if !strings.HasSuffix(matches[len(matches)-1], "20260115-000000") {
		t.Fatalf("newest backup missing; got %v", matches)
	}
}

func TestPruneBackupsDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("%s.bak.202601%02d-000000", path, i)
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EZCLOUD_MAX_BACKUPS", "0") // disable pruning
	pruneBackups(path)
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != 12 {
		t.Fatalf("pruning should be disabled; expected 12, got %d", len(matches))
	}
}
