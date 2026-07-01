package awscreds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOptionTwoProfileBlock(t *testing.T) {
	parsed := Parse(`
[example]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret
aws_session_token = token
`)

	if parsed.ProfileName != "example" {
		t.Fatalf("profile = %q", parsed.ProfileName)
	}
	if parsed.Fields[KeyAccessKeyID] != "AKIAEXAMPLE" {
		t.Fatalf("access key not parsed")
	}
	if parsed.Fields[KeySecretAccessKey] != "secret" {
		t.Fatalf("secret key not parsed")
	}
	if parsed.Fields[KeySessionToken] != "token" {
		t.Fatalf("session token not parsed")
	}
}

func TestParseExportEnvBlock(t *testing.T) {
	parsed := Parse(`
export AWS_ACCESS_KEY_ID="AKIAENV"
export AWS_SECRET_ACCESS_KEY='env-secret'
export AWS_SESSION_TOKEN=env-token
`)

	if parsed.Fields[KeyAccessKeyID] != "AKIAENV" {
		t.Fatalf("env access key not mapped")
	}
	if parsed.Fields[KeySecretAccessKey] != "env-secret" {
		t.Fatalf("env secret key not mapped")
	}
	if parsed.Fields[KeySessionToken] != "env-token" {
		t.Fatalf("env session token not mapped")
	}
}

func TestParseAwsProfileRegionAndOutputEnvBlock(t *testing.T) {
	parsed := Parse(`
export AWS_PROFILE="prod"
export AWS_ACCESS_KEY_ID="AKIAENV"
export AWS_SECRET_ACCESS_KEY='env-secret'
export AWS_DEFAULT_REGION=eu-west-1
export AWS_DEFAULT_OUTPUT=json
export AWS_CA_BUNDLE=/tmp/ca.pem
export AWS_ROLE_ARN=arn:aws:iam::123456789012:role/Admin
export AWS_ROLE_SESSION_NAME=desktop
export AWS_WEB_IDENTITY_TOKEN_FILE=/tmp/token.jwt
export AWS_ENDPOINT_URL=https://example.local
export AWS_RETRY_MODE=adaptive
export AWS_MAX_ATTEMPTS=5
`)

	if parsed.ProfileName != "prod" {
		t.Fatalf("profile = %q", parsed.ProfileName)
	}
	want := map[string]string{
		KeyAccessKeyID:     "AKIAENV",
		KeySecretAccessKey: "env-secret",
		KeyRegion:          "eu-west-1",
		KeyOutput:          "json",
		KeyCABundle:        "/tmp/ca.pem",
		KeyRoleArn:         "arn:aws:iam::123456789012:role/Admin",
		KeyRoleSessionName: "desktop",
		KeyWebIdentityFile: "/tmp/token.jwt",
		KeyEndpointURL:     "https://example.local",
		KeyRetryMode:       "adaptive",
		KeyMaxAttempts:     "5",
	}
	for key, value := range want {
		if parsed.Fields[key] != value {
			t.Fatalf("%s = %q, want %q", key, parsed.Fields[key], value)
		}
	}
}

func TestParseProfilePrefixedConfigBlock(t *testing.T) {
	parsed := Parse(`
[profile staging]
region = eu-north-1
output = yaml
sso_session = corp
sso_account_id = 123456789012
sso_role_name = Developer
`)

	if parsed.ProfileName != "staging" {
		t.Fatalf("profile = %q", parsed.ProfileName)
	}
	want := map[string]string{
		KeyRegion:       "eu-north-1",
		KeyOutput:       "yaml",
		KeySSOSession:   "corp",
		KeySSOAccountID: "123456789012",
		KeySSORoleName:  "Developer",
	}
	for key, value := range want {
		if parsed.Fields[key] != value {
			t.Fatalf("%s = %q, want %q", key, parsed.Fields[key], value)
		}
	}
}

func TestSaveGetDeleteProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("[default]\naws_access_key_id = OLD\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fields := map[string]string{
		KeyAccessKeyID:     "NEW",
		KeySecretAccessKey: "SECRET",
		KeySessionToken:    "TOKEN",
	}
	if err := Save(path, "work", fields); err != nil {
		t.Fatal(err)
	}

	got, err := Get(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[KeyAccessKeyID] != "NEW" || got.Fields[KeySecretAccessKey] != "SECRET" || got.Fields[KeySessionToken] != "TOKEN" {
		t.Fatalf("unexpected fields: %#v", got.Fields)
	}

	if err := Delete(path, "work"); err != nil {
		t.Fatal(err)
	}
	got, err = Get(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fields) != 0 {
		t.Fatalf("profile was not deleted: %#v", got.Fields)
	}
}

func TestSaveUpdatesExistingProfileInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	initial := `# keep preamble
[work]
aws_access_key_id = OLD
aws_secret_access_key = OLDSECRET
region = eu-north-1

[other]
aws_access_key_id = OTHER
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	fields := map[string]string{
		KeyAccessKeyID:     "NEW",
		KeySecretAccessKey: "NEWSECRET",
		"region":           "eu-west-1",
	}
	if err := Save(path, "work", fields); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "[work]") != 1 {
		t.Fatalf("work profile duplicated or missing:\n%s", text)
	}
	for _, want := range []string{
		"# keep preamble",
		"aws_access_key_id = NEW",
		"aws_secret_access_key = NEWSECRET",
		"region = eu-west-1",
		"[other]",
		"aws_access_key_id = OTHER",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "OLDSECRET") {
		t.Fatalf("old secret was not updated:\n%s", text)
	}
}
