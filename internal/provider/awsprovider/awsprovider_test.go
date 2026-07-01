package awsprovider

import (
	"path/filepath"
	"testing"

	"ez-cloud-manager/internal/provider"
)

func TestRegisteredAsAWS(t *testing.T) {
	p, err := provider.Get("aws")
	if err != nil {
		t.Fatalf("provider.Get(aws): %v", err)
	}
	if p.ID() != "aws" || p.DisplayName() != "AWS" {
		t.Fatalf("unexpected identity: id=%q name=%q", p.ID(), p.DisplayName())
	}
}

func TestRoundTrip(t *testing.T) {
	p := New()
	path := filepath.Join(t.TempDir(), "credentials")

	// Empty store lists nothing.
	if got, err := p.List(path); err != nil || len(got) != 0 {
		t.Fatalf("List empty: got=%v err=%v", got, err)
	}

	// Save then read back.
	fields := map[string]string{
		"aws_access_key_id":     "AKIAEXAMPLE",
		"aws_secret_access_key": "secret",
		"region":                "eu-central-1",
	}
	if err := p.Save(path, "dev", fields); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := p.Get(path, "dev")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "dev" {
		t.Fatalf("Get name = %q, want dev", got.Name)
	}
	for k, want := range fields {
		if got.Fields[k] != want {
			t.Fatalf("field %q = %q, want %q", k, got.Fields[k], want)
		}
	}

	summaries, err := p.List(path)
	if err != nil || len(summaries) != 1 || summaries[0].Name != "dev" {
		t.Fatalf("List after save: %v err=%v", summaries, err)
	}

	if err := p.Delete(path, "dev"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, err := p.List(path); err != nil || len(got) != 0 {
		t.Fatalf("List after delete: got=%v err=%v", got, err)
	}
}

func TestParse(t *testing.T) {
	p := New()
	parsed := p.Parse("[staging]\nAWS_ACCESS_KEY_ID=AKIA123\nAWS_SECRET_ACCESS_KEY=shh\n")
	if parsed.ProfileName != "staging" {
		t.Fatalf("ProfileName = %q, want staging", parsed.ProfileName)
	}
	if parsed.Fields["aws_access_key_id"] != "AKIA123" {
		t.Fatalf("access key = %q", parsed.Fields["aws_access_key_id"])
	}
}
