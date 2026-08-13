package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ez-cloud-manager/internal/plugin"
	profilemodel "ez-cloud-manager/internal/profile"
)

func TestAuthorizeLTConnectionAcceptsExplicitAndShowAllPolicies(t *testing.T) {
	e := newCLIEnv(t)
	explicitBlob, err := json.Marshal(profilemodel.CloudAccountsSettings{
		Accounts: []profilemodel.AccountRef{{Provider: "aws", Account: "allowed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := profilemodel.Create(e.dataDir, profilemodel.Profile{
		Name: "explicit",
		Settings: map[string]json.RawMessage{
			plugin.CloudAccountsID: explicitBlob,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeLTConnection(e.dataDir, explicit.ID, "allowed"); err != nil {
		t.Fatalf("explicitly allowed connection rejected: %v", err)
	}
	if _, err := authorizeLTConnection(e.dataDir, explicit.ID, "denied"); err == nil {
		t.Fatal("unlisted connection passed authorization")
	}

	showAllBlob, err := json.Marshal(profilemodel.CloudAccountsSettings{ShowAllAccounts: true})
	if err != nil {
		t.Fatal(err)
	}
	showAll, err := profilemodel.Create(e.dataDir, profilemodel.Profile{
		Name: "show-all",
		Settings: map[string]json.RawMessage{
			plugin.CloudAccountsID: showAllBlob,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeLTConnection(e.dataDir, showAll.ID, "any-valid-name"); err != nil {
		t.Fatalf("explicit show-all connection rejected: %v", err)
	}
}

func TestPrepareLTClientRejectsCredentialProcessAndRoutingOverrides(t *testing.T) {
	root := t.TempDir()
	credentials := filepath.Join(root, "credentials")
	config := filepath.Join(root, "config")
	if err := os.WriteFile(credentials, []byte(`[prod]
aws_access_key_id = AKIATEST
aws_secret_access_key = not-a-real-secret
credential_process = /tmp/unreviewed-helper
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[profile prod]\nregion = us-east-1\nendpoint_url = https://example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_CONFIG_FILE", config)
	if _, _, err := prepareLTClient("prod", "us-east-1"); err == nil {
		t.Fatal("Launch Templates accepted a profile with credential_process/routing overrides")
	}
}

func TestPrepareLTClientUsesPrivateSanitizedSnapshot(t *testing.T) {
	root := t.TempDir()
	credentials := filepath.Join(root, "credentials")
	config := filepath.Join(root, "config")
	if err := os.WriteFile(credentials, []byte(`[prod]
aws_access_key_id = AKIATEST
aws_secret_access_key = not-a-real-secret
aws_session_token = short-lived
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[profile prod]\nregion = us-east-1\noutput = json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_CONFIG_FILE", config)
	client, cleanup, err := prepareLTClient("prod", "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	sanitizedCredentials := client.Environment["AWS_SHARED_CREDENTIALS_FILE"]
	sanitizedConfig := client.Environment["AWS_CONFIG_FILE"]
	if sanitizedCredentials == credentials || sanitizedConfig == config {
		t.Fatal("Launch Templates retained mutable source AWS paths")
	}
	for _, path := range []string{sanitizedCredentials, sanitizedConfig} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("sanitized AWS file %s mode = %o, want 0600", path, info.Mode().Perm())
		}
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sanitizedCredentials); !os.IsNotExist(err) {
		t.Fatalf("sanitized AWS snapshot survived cleanup: %v", err)
	}
}

func TestExecuteLTWithCleanupRemovesSnapshotOnCommandError(t *testing.T) {
	cleaned := false
	err := executeLTWithCleanup(
		func() error { return os.ErrInvalid },
		func() error { cleaned = true; return nil },
	)
	if !cleaned {
		t.Fatal("isolated AWS snapshot cleanup was skipped after an LT error")
	}
	if err == nil || !strings.Contains(err.Error(), os.ErrInvalid.Error()) {
		t.Fatalf("command error = %v, want original error", err)
	}
}

func TestLTRequiresWorkspaceID(t *testing.T) {
	e := newCLIEnv(t)
	_, stderr, code := e.run(t, "lt", "templates", "--profile", "prod", "--region", "us-east-1")
	if code == 0 {
		t.Fatal("lt templates accepted a request without --workspace")
	}
	if !strings.Contains(stderr, "--workspace, --profile and --region are required") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestLTRejectsConnectionOutsideWorkspaceBeforeAWSExecution(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "prod-workspace")
	if _, err := profilemodel.AddConnectionRef(filepath.Join(e.dataDir, "profiles"), workspace.ID, profilemodel.AccountRef{
		Provider: "aws", Account: "allowed",
	}); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := e.run(
		t,
		"lt", "templates",
		"--workspace", workspace.ID,
		"--profile", "denied",
		"--region", "us-east-1",
	)
	if code == 0 {
		t.Fatal("lt templates accepted a Connection outside the Workspace scope")
	}
	if !strings.Contains(stderr, `AWS connection "denied" is not allowed in workspace "prod-workspace"`) {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	if strings.Contains(stderr, "AWS CLI") {
		t.Fatalf("rejected request reached the AWS client boundary: %s", stderr)
	}
}

func TestLTRejectsUnknownWorkspaceBeforeAWSExecution(t *testing.T) {
	e := newCLIEnv(t)
	_, stderr, code := e.run(
		t,
		"lt", "templates",
		"--workspace", "does-not-exist",
		"--profile", "prod",
		"--region", "us-east-1",
	)
	if code == 0 {
		t.Fatal("lt templates accepted an unknown Workspace")
	}
	if !strings.Contains(stderr, `load workspace: profile "does-not-exist" not found`) {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	if strings.Contains(stderr, "AWS CLI") {
		t.Fatalf("unknown Workspace reached the AWS client boundary: %s", stderr)
	}
}

func TestVerifyLTWorkspaceEnvironmentBindsCredentialStore(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", "/stores/a/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/stores/a/credentials")
	workspace := profilemodel.Profile{EnvVars: []profilemodel.EnvVar{
		{Key: "AWS_CONFIG_FILE", Value: "/stores/a/config"},
		{Key: "AWS_SHARED_CREDENTIALS_FILE", Value: "/stores/a/credentials"},
	}}
	if err := verifyLTWorkspaceEnvironment(workspace); err != nil {
		t.Fatalf("matching workspace environment rejected: %v", err)
	}

	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/stores/b/credentials")
	if err := verifyLTWorkspaceEnvironment(workspace); err == nil {
		t.Fatal("mismatched credential store was accepted")
	}
}

func TestVerifyLTWorkspaceEnvironmentRejectsAmbientOverride(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", "/ambient/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	if err := verifyLTWorkspaceEnvironment(profilemodel.Profile{}); err == nil {
		t.Fatal("ambient AWS_CONFIG_FILE bypassed an empty Workspace context")
	}
}
