package awsprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestCheckRejectsMalformedSuccessfulAWSOutput(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credentials, []byte("[profile]\naws_access_key_id = AKIAEXAMPLE\naws_secret_access_key = secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "aws")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'not-json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	result, err := New().(provider.Checker).Check(
		context.Background(),
		credentials,
		"profile",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error != "AWS CLI returned malformed identity JSON" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestListsAndReadsSafeSSOProfilesFromSharedConfigAsReadOnly(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	config := filepath.Join(dir, "config")
	data := []byte(`[profile prod-readonly]
sso_session = company
sso_account_id = 123456789012
sso_role_name = ReadOnly
region = eu-west-1

[sso-session company]
sso_start_url = https://example.awsapps.com/start
sso_region = eu-west-1
`)
	if err := os.WriteFile(config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	profiles, err := p.List(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Name != "prod-readonly" || !profiles[0].ReadOnly || profiles[0].Source != "sso" {
		t.Fatalf("unexpected summaries: %+v", profiles)
	}
	got, err := p.Get(credentials, "prod-readonly")
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields["sso_account_id"] != "123456789012" || got.Fields["sso_session"] != "company" {
		t.Fatalf("unexpected profile: %+v", got)
	}
	if err := p.Save(credentials, "prod-readonly", got.Fields); err == nil {
		t.Fatal("expected SSO profile save to be refused")
	}
	if err := p.Delete(credentials, "prod-readonly"); err == nil {
		t.Fatal("expected SSO profile delete to be refused")
	}
}

func TestBlocksAutoDiscoveredSSOProfilesWithCredentialOrEndpointOverrides(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	config := filepath.Join(dir, "config")
	data := []byte(`[profile helper]
sso_session = company
sso_account_id = 123456789012
sso_role_name = ReadOnly
credential_process = /tmp/unreviewed-helper

[profile redirected]
sso_session = company
sso_account_id = 123456789012
sso_role_name = ReadOnly
endpoint_url = https://attacker.invalid

[profile mixed-login]
sso_session = company
sso_account_id = 123456789012
sso_role_name = ReadOnly
login_session = local-login

[sso-session company]
sso_start_url = https://example.awsapps.com/start
sso_region = eu-west-1
`)
	if err := os.WriteFile(config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	profiles, err := p.List(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("unsafe SSO profiles must not enter the provider rail: %+v", profiles)
	}
	for _, name := range []string{"helper", "redirected", "mixed-login"} {
		if _, err := p.Get(credentials, name); err == nil {
			t.Fatalf("Get(%q) unexpectedly accepted unsafe SSO overrides", name)
		}
		result, err := p.(provider.Checker).Check(context.Background(), credentials, name)
		if err != nil {
			t.Fatal(err)
		}
		if result.OK || !strings.Contains(result.Error, "safe verification") {
			t.Fatalf("Check(%q) did not fail closed: %+v", name, result)
		}
	}
}

func TestCheckUsesSanitizedSSOSnapshotAndValidatesAccount(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte(`[profile prod]
sso_session = company
sso_account_id = 123456789012
sso_role_name = ReadOnly
region = eu-west-1

[sso-session company]
sso_start_url = https://example.awsapps.com/start
sso_region = eu-west-1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "aws")
	script := `#!/bin/sh
case "$AWS_CONFIG_FILE" in *kervik-aws-check-*/config) ;; *) exit 4 ;; esac
case "$AWS_SHARED_CREDENTIALS_FILE" in *kervik-aws-check-*/credentials) ;; *) exit 4 ;; esac
test ! -s "$AWS_SHARED_CREDENTIALS_FILE" || exit 4
/usr/bin/grep -q 'sso_account_id = 123456789012' "$AWS_CONFIG_FILE" || exit 4
test "$1" = sts || exit 4
test "$2" = get-caller-identity || exit 4
test "$3" = --profile || exit 4
test "$4" = prod || exit 4
printf '{"Account":"123456789012","Arn":"arn:aws:sts::123456789012:assumed-role/ReadOnly/user","UserId":"id"}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	result, err := New().(provider.Checker).Check(context.Background(), credentials, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Identity["account"] != "123456789012" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckRejectsSSOAccountMismatch(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte(`[profile prod]
sso_start_url = https://example.awsapps.com/start
sso_region = eu-west-1
sso_account_id = 123456789012
sso_role_name = ReadOnly
`), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "aws")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '{\"Account\":\"210987654321\",\"Arn\":\"arn:aws:sts::210987654321:assumed-role/ReadOnly/user\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	result, err := New().(provider.Checker).Check(context.Background(), credentials, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || !strings.Contains(result.Error, "different account") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestLegacySSOProfileCannotBeSavedIntoCredentialsStore(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte(`[profile legacy]
sso_start_url = https://example.awsapps.com/start
sso_region = eu-west-1
sso_account_id = 123456789012
sso_role_name = ReadOnly
`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	got, err := p.Get(credentials, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save(credentials, "legacy", got.Fields); err == nil {
		t.Fatal("legacy SSO profile was written into the credentials store")
	}
	if err := p.(provider.ConditionalSaver).SaveIfUnchanged(
		credentials,
		"legacy",
		got.Fields,
		got.Fields,
		false,
	); err == nil {
		t.Fatal("conditional save accepted a legacy SSO profile")
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
