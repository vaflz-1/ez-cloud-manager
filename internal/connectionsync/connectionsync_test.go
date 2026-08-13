package connectionsync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ez-cloud-manager/internal/gcpcreds"
	"ez-cloud-manager/internal/provider/gcpprovider"
)

type recordedCommand struct {
	Executable string
	Args       []string
	Env        []string
}

type fakeRunner struct {
	calls   []recordedCommand
	handler func(executable string, args, env []string) ([]byte, error)
}

type cancelOnProjectsRunner struct {
	cancel  context.CancelFunc
	calls   []recordedCommand
	cleaned bool
}

func (r *cancelOnProjectsRunner) Run(ctx context.Context, executable string, args, env []string) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{
		Executable: executable, Args: append([]string(nil), args...), Env: append([]string(nil), env...),
	})
	switch {
	case len(args) > 1 && args[0] == "auth" && args[1] == "list":
		return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
	case len(args) > 1 && args[0] == "projects" && args[1] == "list":
		r.cancel()
		return nil, context.Canceled
	case len(args) > 2 && args[0] == "config" && args[1] == "configurations" && args[2] == "delete":
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.cleaned = true
		return nil, nil
	default:
		return nil, nil
	}
}

func (f *fakeRunner) Run(_ context.Context, executable string, args, env []string) ([]byte, error) {
	f.calls = append(f.calls, recordedCommand{
		Executable: executable,
		Args:       append([]string(nil), args...),
		Env:        append([]string(nil), env...),
	})
	if f.handler == nil {
		return nil, nil
	}
	return f.handler(executable, args, env)
}

func newTestManager(t *testing.T, runner Runner, awsConfig, awsCredentials, gcpRoot string) *Manager {
	t.Helper()
	manager, err := New(runner, awsConfig, awsCredentials, gcpRoot, gcpprovider.New())
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func candidateByName(t *testing.T, snapshot DiscoverySnapshot, name string) Candidate {
	t.Helper()
	for _, candidate := range snapshot.Candidates {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("candidate %q not found in %+v", name, snapshot.Candidates)
	return Candidate{}
}

func TestAWSDiscoveryModernLegacyPartialAndCredentialsCollision(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config")
	credentialsPath := filepath.Join(root, "credentials")
	writeTestFile(t, configPath, `[profile modern-a]
sso_session = corp
sso_account_id = 123456789012
sso_role_name = ReadOnly
region = eu-west-1

[profile modern-b]
sso_session = corp
sso_account_id = 123456789012
sso_role_name = Admin

[sso-session corp]
sso_start_url = https://company.awsapps.com/start
sso_region = eu-west-1

[profile legacy]
sso_start_url = https://legacy.awsapps.com/start
sso_region = us-east-1
sso_account_id = 210987654321
sso_role_name = Developer

[profile partial]
sso_session = missing

[profile static-only]
region = eu-central-1
`)
	writeTestFile(t, credentialsPath, `[modern-b]
aws_access_key_id = AKIAEXAMPLEONLY
aws_secret_access_key = not-a-real-secret
`)
	manager := newTestManager(t, &fakeRunner{}, configPath, credentialsPath, filepath.Join(root, "gcloud"))

	snapshot, err := manager.Discover(context.Background(), "aws", "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProtocolVersion != ProtocolVersion || snapshot.Provider != "aws" || snapshot.Revision == "" {
		t.Fatalf("unexpected snapshot metadata: %+v", snapshot)
	}
	if len(snapshot.Candidates) != 4 {
		t.Fatalf("candidates = %+v", snapshot.Candidates)
	}
	if got := candidateByName(t, snapshot, "modern-a"); !got.CanApply || got.AuthMode != "sso-session" {
		t.Fatalf("modern candidate = %+v", got)
	}
	if got := candidateByName(t, snapshot, "legacy"); !got.CanApply || got.AuthMode != "legacy" {
		t.Fatalf("legacy candidate = %+v", got)
	}
	if got := candidateByName(t, snapshot, "partial"); got.CanApply || got.Reason == "" {
		t.Fatalf("partial candidate must be blocked: %+v", got)
	}
	if got := candidateByName(t, snapshot, "modern-b"); got.CanApply || !strings.Contains(got.Reason, "credentials-file") {
		t.Fatalf("credentials collision must be blocked: %+v", got)
	}
	wire, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"company.awsapps.com", "legacy.awsapps.com", "AKIAEXAMPLEONLY", "not-a-real-secret"} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("secret/auth material %q leaked into discovery JSON: %s", forbidden, wire)
		}
	}
}

func TestAWSLoginGroupsSessionsAndPinsExactArgv(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config")
	credentialsPath := filepath.Join(root, "credentials")
	writeTestFile(t, configPath, `[profile alpha]
sso_session = corp
sso_account_id = 123456789012
sso_role_name = ReadOnly
[profile beta]
sso_session = corp
sso_account_id = 123456789012
sso_role_name = Admin
[sso-session corp]
sso_start_url = https://company.awsapps.com/start
sso_region = eu-west-1
[profile legacy]
sso_start_url = https://legacy.awsapps.com/start
sso_region = us-east-1
sso_account_id = 210987654321
sso_role_name = Developer
`)
	var isolationChecks int
	var isolationErrors []string
	runner := &fakeRunner{handler: func(_ string, args, env []string) ([]byte, error) {
		configValue := envValue(env, "AWS_CONFIG_FILE")
		credentialsValue := envValue(env, "AWS_SHARED_CREDENTIALS_FILE")
		if configValue == "" || configValue == configPath || credentialsValue == "" || credentialsValue == credentialsPath {
			isolationErrors = append(isolationErrors, "CLI did not receive isolated paths")
		} else {
			if info, err := os.Stat(configValue); err != nil || info.Mode().Perm() != 0o600 {
				isolationErrors = append(isolationErrors, "sanitized config missing or insecure")
			}
			if info, err := os.Stat(credentialsValue); err != nil || info.Mode().Perm() != 0o600 || info.Size() != 0 {
				isolationErrors = append(isolationErrors, "empty credentials file missing or insecure")
			}
			if info, err := os.Stat(filepath.Dir(configValue)); err != nil || info.Mode().Perm() != 0o700 {
				isolationErrors = append(isolationErrors, "sanitized directory missing or insecure")
			}
			data, err := os.ReadFile(configValue)
			if err != nil {
				isolationErrors = append(isolationErrors, "sanitized config unreadable")
			} else {
				content := string(data)
				for _, forbidden := range []string{"credential_process", "endpoint_url", "aws_access_key_id", "not-a-real-secret"} {
					if strings.Contains(content, forbidden) {
						isolationErrors = append(isolationErrors, "unsafe field in sanitized config: "+forbidden)
					}
				}
			}
		}
		isolationChecks++
		if len(args) > 0 && args[0] == "sts" {
			account := "123456789012"
			if slices.Contains(args, "legacy") {
				account = "210987654321"
			}
			return []byte(`{"Account":"` + account + `"}`), nil
		}
		return nil, nil
	}}
	manager := newTestManager(t, runner, configPath, credentialsPath, filepath.Join(root, "gcloud"))
	snapshot, err := manager.Discover(context.Background(), "aws", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{
		candidateByName(t, snapshot, "alpha").ID,
		candidateByName(t, snapshot, "beta").ID,
		candidateByName(t, snapshot, "legacy").ID,
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "must-not-propagate")
	response, err := manager.Login(context.Background(), "aws", LoginRequest{
		ExpectedRevision: snapshot.Revision,
		CandidateIDs:     ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.LoggedIn != 3 {
		t.Fatalf("response = %+v", response)
	}
	wantArgs := [][]string{
		{"sso", "login", "--sso-session", "corp"},
		{"sso", "login", "--profile", "legacy"},
		{"sts", "get-caller-identity", "--profile", "alpha", "--output", "json", "--no-cli-pager"},
		{"sts", "get-caller-identity", "--profile", "beta", "--output", "json", "--no-cli-pager"},
		{"sts", "get-caller-identity", "--profile", "legacy", "--output", "json", "--no-cli-pager"},
	}
	if len(runner.calls) != len(wantArgs) {
		t.Fatalf("calls = %+v", runner.calls)
	}
	if isolationChecks != len(wantArgs) || len(isolationErrors) != 0 {
		t.Fatalf("AWS isolation checks=%d errors=%v", isolationChecks, isolationErrors)
	}
	for i, call := range runner.calls {
		if call.Executable != "aws" || !slices.Equal(call.Args, wantArgs[i]) {
			t.Fatalf("call[%d] = %+v, want argv %v", i, call, wantArgs[i])
		}
		joinedEnv := strings.Join(call.Env, "\n")
		if strings.Contains(joinedEnv, "AWS_ACCESS_KEY_ID=") {
			t.Fatalf("ambient AWS credentials propagated: %v", call.Env)
		}
		configValue := envValue(call.Env, "AWS_CONFIG_FILE")
		credentialsValue := envValue(call.Env, "AWS_SHARED_CREDENTIALS_FILE")
		if configValue == "" || configValue == configPath || credentialsValue == "" || credentialsValue == credentialsPath {
			t.Fatalf("AWS CLI did not receive isolated config/credentials: %v", call.Env)
		}
	}
	configValue := envValue(runner.calls[0].Env, "AWS_CONFIG_FILE")
	credentialsValue := envValue(runner.calls[0].Env, "AWS_SHARED_CREDENTIALS_FILE")
	if _, err := os.Stat(configValue); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated config was not cleaned: %v", err)
	}
	if _, err := os.Stat(credentialsValue); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated credentials were not cleaned: %v", err)
	}
}

func TestAWSDiscoveryBlocksMixedSSOAndLoginSessionCredentialProviders(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config")
	writeTestFile(t, configPath, `[profile mixed]
sso_session = corp
sso_account_id = 123456789012
sso_role_name = ReadOnly
login_session = local-login

[sso-session corp]
sso_start_url = https://company.awsapps.com/start
sso_region = eu-west-1
`)
	manager := newTestManager(t, &fakeRunner{}, configPath, filepath.Join(root, "credentials"), filepath.Join(root, "gcloud"))
	snapshot, err := manager.Discover(context.Background(), "aws", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateByName(t, snapshot, "mixed")
	if candidate.CanApply || !strings.Contains(candidate.Reason, "credential source") {
		t.Fatalf("mixed credential providers were not blocked: %+v", candidate)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func TestAWSLoginRejectsIdentityMismatchAndCleansIsolation(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config")
	credentialsPath := filepath.Join(root, "credentials")
	writeTestFile(t, configPath, `[profile prod]
sso_session = corp
sso_account_id = 123456789012
sso_role_name = ReadOnly
[sso-session corp]
sso_start_url = https://company.awsapps.com/start
sso_region = eu-west-1
`)
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "sts" {
			return []byte(`{"Account":"999999999999"}`), nil
		}
		return nil, nil
	}}
	manager := newTestManager(t, runner, configPath, credentialsPath, filepath.Join(root, "gcloud"))
	snapshot, err := manager.Discover(context.Background(), "aws", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Login(context.Background(), "aws", LoginRequest{
		ExpectedRevision: snapshot.Revision,
		CandidateIDs:     []string{snapshot.Candidates[0].ID},
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v", runner.calls)
	}
	for _, call := range runner.calls {
		configValue := envValue(call.Env, "AWS_CONFIG_FILE")
		credentialsValue := envValue(call.Env, "AWS_SHARED_CREDENTIALS_FILE")
		if _, err := os.Stat(configValue); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("isolated config was not cleaned: %v", err)
		}
		if _, err := os.Stat(credentialsValue); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("isolated credentials were not cleaned: %v", err)
		}
	}
}

func TestGCPDiscoverAndApplyUsesConditionalDestinationState(t *testing.T) {
	root := t.TempDir()
	if err := gcpcreds.Save(root, "alpha-project", map[string]string{
		gcpcreds.KeyAccount: "alice@example.com", gcpcreds.KeyProject: "alpha-project",
		gcpcreds.KeyRegion: "europe-west1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := gcpcreds.Save(root, "gamma-project", map[string]string{
		gcpcreds.KeyAccount: "old@example.com", gcpcreds.KeyProject: "old-project",
	}); err != nil {
		t.Fatal(err)
	}
	projectsJSON := `[
{"projectId":"alpha-project","name":"Alpha","lifecycleState":"ACTIVE"},
{"projectId":"beta-project","name":"Beta","lifecycleState":"ACTIVE"},
{"projectId":"gamma-project","name":"Gamma","lifecycleState":"ACTIVE"}
]`
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		switch {
		case slices.Equal(args, []string{"auth", "list", "--filter=status:ACTIVE", "--format=json(account,status)"}):
			return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
		case slices.Equal(args, []string{"auth", "list", "--format=json(account,status)"}):
			return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
		case slices.Equal(args, []string{"config", "configurations", "create", "kervik-auth-fixednonce", "--no-activate", "--quiet"}),
			slices.Equal(args, []string{"config", "set", "core/account", "alice@example.com", "--configuration=kervik-auth-fixednonce", "--quiet"}),
			slices.Equal(args, []string{"config", "configurations", "delete", "kervik-auth-fixednonce", "--quiet"}):
			return nil, nil
		case slices.Equal(args, []string{"projects", "list", "--configuration=kervik-auth-fixednonce", "--filter=lifecycleState:ACTIVE", "--format=json(projectId,name,lifecycleState)"}):
			return []byte(projectsJSON), nil
		default:
			return nil, errors.New("unexpected fake gcloud argv")
		}
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	manager.nonce = func() (string, error) { return "fixednonce", nil }

	snapshot, err := manager.Discover(context.Background(), "gcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if candidateByName(t, snapshot, "alpha-project").Status != StatusUnchanged ||
		candidateByName(t, snapshot, "beta-project").Status != StatusNew ||
		candidateByName(t, snapshot, "gamma-project").Status != StatusUpdate {
		t.Fatalf("wrong candidate statuses: %+v", snapshot.Candidates)
	}
	if replacement := candidateByName(t, snapshot, "gamma-project"); replacement.CanApply || replacement.Reason == "" {
		t.Fatalf("material identity replacement was not blocked for review: %+v", replacement)
	}
	var guardedProvider, guardedStore string
	var guardedNames []string
	response, err := manager.ApplyGuarded(context.Background(), "gcp", ApplyRequest{
		ExpectedRevision: snapshot.Revision,
		Principal:        snapshot.Principal,
		Mode:             ModeSelected,
		CandidateIDs: []string{
			candidateByName(t, snapshot, "beta-project").ID,
		},
	}, func(providerID, storePath string, names []string) error {
		guardedProvider = providerID
		guardedStore = storePath
		guardedNames = append([]string(nil), names...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if guardedProvider != "gcp" || guardedStore != root || !slices.Equal(guardedNames, []string{"beta-project"}) {
		t.Fatalf("create guard got provider=%q store=%q names=%v", guardedProvider, guardedStore, guardedNames)
	}
	if response.Added != 1 || response.Updated != 0 || response.Unchanged != 0 || len(response.Results) != 1 {
		t.Fatalf("apply response = %+v", response)
	}
	beta, err := gcpcreds.Get(root, "beta-project")
	if err != nil {
		t.Fatal(err)
	}
	if beta.Fields[gcpcreds.KeyAccount] != "alice@example.com" || beta.Fields[gcpcreds.KeyProject] != "beta-project" {
		t.Fatalf("profile beta-project = %+v", beta.Fields)
	}
	gamma, err := gcpcreds.Get(root, "gamma-project")
	if err != nil {
		t.Fatal(err)
	}
	if gamma.Fields[gcpcreds.KeyAccount] != "old@example.com" || gamma.Fields[gcpcreds.KeyProject] != "old-project" {
		t.Fatalf("blocked identity replacement changed gamma-project: %+v", gamma.Fields)
	}
	alpha, err := gcpcreds.Get(root, "alpha-project")
	if err != nil {
		t.Fatal(err)
	}
	if alpha.Fields[gcpcreds.KeyRegion] != "europe-west1" {
		t.Fatalf("unrelated existing fields were not preserved: %+v", alpha.Fields)
	}
}

func TestGCPMaterialIdentityChangeClassification(t *testing.T) {
	base := gcpCandidateState{
		Expected: map[string]string{
			gcpcreds.KeyAccount: "alice@example.com",
			gcpcreds.KeyProject: "alpha-project",
			gcpcreds.KeyRegion:  "europe-west1",
		},
		Desired: map[string]string{
			gcpcreds.KeyAccount: "alice@example.com",
			gcpcreds.KeyProject: "alpha-project",
			gcpcreds.KeyRegion:  "us-central1",
		},
	}
	tests := []struct {
		name  string
		state gcpCandidateState
		want  bool
	}{
		{name: "ordinary metadata update remains eligible", state: base},
		{
			name: "new destination is not a replacement",
			state: gcpCandidateState{
				ExpectAbsent: true,
				Desired: map[string]string{
					gcpcreds.KeyAccount: "alice@example.com",
					gcpcreds.KeyProject: "alpha-project",
				},
			},
		},
		{
			name: "principal replacement is blocked",
			state: gcpCandidateState{
				Expected: base.Expected,
				Desired: map[string]string{
					gcpcreds.KeyAccount: "bob@example.com",
					gcpcreds.KeyProject: "alpha-project",
				},
			},
			want: true,
		},
		{
			name: "project replacement is blocked",
			state: gcpCandidateState{
				Expected: base.Expected,
				Desired: map[string]string{
					gcpcreds.KeyAccount: "alice@example.com",
					gcpcreds.KeyProject: "other-project",
				},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := gcpMaterialIdentityChanged(tc.state); got != tc.want {
				t.Fatalf("gcpMaterialIdentityChanged() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestGCPCreateGuardFailurePreventsProviderWrites(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		switch {
		case len(args) > 1 && args[0] == "auth" && args[1] == "list":
			return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
		case len(args) > 1 && args[0] == "projects" && args[1] == "list":
			return []byte(`[{"projectId":"brand-new-project","name":"New","lifecycleState":"ACTIVE"}]`), nil
		default:
			return nil, nil
		}
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	manager.nonce = func() (string, error) { return "guard", nil }
	snapshot, err := manager.Discover(context.Background(), "gcp", "")
	if err != nil {
		t.Fatal(err)
	}
	guardErr := errors.New("scope store unavailable")
	_, err = manager.ApplyGuarded(context.Background(), "gcp", ApplyRequest{
		ExpectedRevision: snapshot.Revision,
		Principal:        snapshot.Principal,
		Mode:             ModeSelected,
		CandidateIDs:     []string{snapshot.Candidates[0].ID},
	}, func(providerID, storePath string, names []string) error {
		if providerID != "gcp" || storePath != root || !slices.Equal(names, []string{"brand-new-project"}) {
			t.Fatalf("unexpected create guard input: provider=%q store=%q names=%v", providerID, storePath, names)
		}
		return guardErr
	})
	if !errors.Is(err, guardErr) {
		t.Fatalf("apply error = %v, want create guard error", err)
	}
	profiles, listErr := gcpcreds.List(root)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(profiles) != 0 {
		t.Fatalf("provider records written after create guard failure: %+v", profiles)
	}
}

func TestGCPIdentityReplacementRejectedBeforeGuardOrWrite(t *testing.T) {
	root := t.TempDir()
	const name = "alpha-project"
	if err := gcpcreds.Save(root, name, map[string]string{
		gcpcreds.KeyAccount: "old-owner@example.com",
		gcpcreds.KeyProject: name,
		gcpcreds.KeyRegion:  "europe-west1",
	}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		switch {
		case len(args) > 1 && args[0] == "auth" && args[1] == "list":
			return []byte(`[{"account":"new-owner@example.com","status":"ACTIVE"}]`), nil
		case len(args) > 1 && args[0] == "projects" && args[1] == "list":
			return []byte(`[{"projectId":"alpha-project","name":"Alpha","lifecycleState":"ACTIVE"}]`), nil
		default:
			return nil, nil
		}
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	manager.nonce = func() (string, error) { return "replacement", nil }
	snapshot, err := manager.Discover(context.Background(), "gcp", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateByName(t, snapshot, name)
	if candidate.Status != StatusUpdate {
		t.Fatalf("candidate status = %q, want %q", candidate.Status, StatusUpdate)
	}
	guardCalled := false
	_, err = manager.ApplyGuarded(context.Background(), "gcp", ApplyRequest{
		ExpectedRevision: snapshot.Revision,
		Principal:        snapshot.Principal,
		Mode:             ModeSelected,
		CandidateIDs:     []string{candidate.ID},
	}, func(providerID, storePath string, names []string) error {
		guardCalled = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to replace") {
		t.Fatalf("apply error = %v, want fail-closed identity replacement error", err)
	}
	// The guard and provider store use independent locks. Treat this assertion
	// as the regression barrier: replacement must stop before the guard, so no
	// cleanup-unlock/write interleaving can ever authorize the new principal.
	if guardCalled {
		t.Fatal("identity replacement reached the new-connection scope guard")
	}
	after, err := gcpcreds.Get(root, name)
	if err != nil {
		t.Fatal(err)
	}
	if after.Fields[gcpcreds.KeyAccount] != "old-owner@example.com" ||
		after.Fields[gcpcreds.KeyProject] != name ||
		after.Fields[gcpcreds.KeyRegion] != "europe-west1" {
		t.Fatalf("existing configuration changed after rejected replacement: %+v", after.Fields)
	}
}

func TestGCPApplyRejectsChangedDiscoveryRevision(t *testing.T) {
	root := t.TempDir()
	projectsJSON := `[{"projectId":"alpha-project","name":"Alpha","lifecycleState":"ACTIVE"}]`
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		if args[0] == "auth" {
			return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
		}
		return []byte(projectsJSON), nil
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	snapshot, err := manager.Discover(context.Background(), "gcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := gcpcreds.Save(root, "alpha-project", map[string]string{
		gcpcreds.KeyAccount: "someone-else@example.com", gcpcreds.KeyProject: "alpha-project",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Apply(context.Background(), "gcp", ApplyRequest{
		ExpectedRevision: snapshot.Revision,
		Principal:        snapshot.Principal,
		Mode:             ModeSelected,
		CandidateIDs:     []string{snapshot.Candidates[0].ID},
	})
	if !errors.Is(err, ErrSnapshotChanged) {
		t.Fatalf("apply error = %v, want ErrSnapshotChanged", err)
	}
}

func TestUnsafeGCPRoutingOverridesAreBlocked(t *testing.T) {
	for _, key := range []string{
		"auth.credential_file_override",
		"auth.login_config_file",
		"auth.impersonate_service_account",
		"auth.access_token_file",
		"proxy.address",
		"api_endpoint_overrides.compute",
		"context_aware.use_client_certificate",
		"core.custom_ca_certs_file",
		"core.universe_domain",
		"future.identity_endpoint_override",
	} {
		t.Run(strings.ReplaceAll(key, ".", "_"), func(t *testing.T) {
			if !hasUnsafeGCPRoutingOverride(map[string]string{key: "configured"}) {
				t.Fatalf("routing override %q was not classified as unsafe", key)
			}
		})
	}
	if hasUnsafeGCPRoutingOverride(map[string]string{
		gcpcreds.KeyAccount: "alice@example.com",
		gcpcreds.KeyProject: "safe-project",
		gcpcreds.KeyRegion:  "europe-west1",
	}) {
		t.Fatal("ordinary account/project/region fields were classified as unsafe")
	}
}

func TestGCPExistingCredentialOverrideCannotBeSynced(t *testing.T) {
	root := t.TempDir()
	const projectID = "blocked-project"
	original := map[string]string{
		gcpcreds.KeyAccount:                "alice@example.com",
		gcpcreds.KeyProject:                projectID,
		"auth.impersonate_service_account": "router@example.com",
	}
	if err := gcpcreds.Save(root, projectID, original); err != nil {
		t.Fatal(err)
	}
	projectsJSON := `[{"projectId":"blocked-project","name":"Blocked","lifecycleState":"ACTIVE"}]`
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "auth" {
			return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
		}
		return []byte(projectsJSON), nil
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	snapshot, err := manager.Discover(context.Background(), "gcp", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateByName(t, snapshot, projectID)
	if candidate.CanApply || candidate.Reason == "" {
		t.Fatalf("unsafe destination candidate was not blocked: %+v", candidate)
	}
	_, err = manager.Apply(context.Background(), "gcp", ApplyRequest{
		ExpectedRevision: snapshot.Revision,
		Principal:        snapshot.Principal,
		Mode:             ModeSelected,
		CandidateIDs:     []string{candidate.ID},
	})
	if err == nil || !strings.Contains(err.Error(), "manual review") {
		t.Fatalf("blocked apply error = %v", err)
	}
	after, err := gcpcreds.Get(root, projectID)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range original {
		if after.Fields[key] != want {
			t.Fatalf("blocked destination changed: %+v", after.Fields)
		}
	}
}

func TestGCPDiscoveryUsesCleanScratchAndPreservesActiveState(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "active_config")
	adcPath := filepath.Join(root, "application_default_credentials.json")
	writeTestFile(t, activePath, "dangerous-active")
	writeTestFile(t, adcPath, `{"sentinel":"unchanged"}`)
	if err := gcpcreds.Save(root, "dangerous-active", map[string]string{
		gcpcreds.KeyAccount:                "alice@example.com",
		"auth.impersonate_service_account": "router@example.com",
		"proxy.address":                    "127.0.0.1",
		"api_endpoint_overrides.compute":   "https://malicious.invalid",
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		switch {
		case slices.Equal(args, []string{"auth", "list", "--filter=status:ACTIVE", "--format=json(account,status)"}):
			return []byte(`[{"account":"alice@example.com","status":"ACTIVE"}]`), nil
		case slices.Equal(args, []string{"config", "configurations", "create", "kervik-auth-isolated", "--no-activate", "--quiet"}),
			slices.Equal(args, []string{"config", "set", "core/account", "alice@example.com", "--configuration=kervik-auth-isolated", "--quiet"}),
			slices.Equal(args, []string{"config", "configurations", "delete", "kervik-auth-isolated", "--quiet"}):
			return nil, nil
		case slices.Equal(args, []string{"projects", "list", "--configuration=kervik-auth-isolated", "--filter=lifecycleState:ACTIVE", "--format=json(projectId,name,lifecycleState)"}):
			return []byte(`[{"projectId":"isolated-project","name":"Isolated","lifecycleState":"ACTIVE"}]`), nil
		default:
			return nil, errors.New("unexpected fake gcloud argv")
		}
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	manager.nonce = func() (string, error) { return "isolated", nil }

	snapshot, err := manager.Discover(context.Background(), "gcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Principal != "alice@example.com" || len(snapshot.Candidates) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	want := [][]string{
		{"auth", "list", "--filter=status:ACTIVE", "--format=json(account,status)"},
		{"config", "configurations", "create", "kervik-auth-isolated", "--no-activate", "--quiet"},
		{"config", "set", "core/account", "alice@example.com", "--configuration=kervik-auth-isolated", "--quiet"},
		{"projects", "list", "--configuration=kervik-auth-isolated", "--filter=lifecycleState:ACTIVE", "--format=json(projectId,name,lifecycleState)"},
		{"config", "configurations", "delete", "kervik-auth-isolated", "--quiet"},
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %+v", runner.calls)
	}
	for i, call := range runner.calls {
		if call.Executable != "gcloud" || !slices.Equal(call.Args, want[i]) {
			t.Fatalf("call[%d] = %+v, want %v", i, call, want[i])
		}
		joinedArgs := strings.Join(call.Args, " ")
		if strings.Contains(joinedArgs, "--account=") || strings.Contains(joinedArgs, "auth login") {
			t.Fatalf("ordinary discovery escaped isolated config: %v", call.Args)
		}
		if envValue(call.Env, "CLOUDSDK_CONFIG") != root {
			t.Fatalf("CLOUDSDK_CONFIG missing from call: %v", call.Env)
		}
	}
	for path, wantContent := range map[string]string{
		activePath: "dangerous-active",
		adcPath:    `{"sentinel":"unchanged"}`,
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != wantContent {
			t.Fatalf("%s changed: got %q err=%v", filepath.Base(path), data, err)
		}
	}
}

func TestGCPDiscoveryCancellationStillDeletesScratch(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	runner := &cancelOnProjectsRunner{cancel: cancel}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	manager.nonce = func() (string, error) { return "cancel", nil }
	_, err := manager.Discover(ctx, "gcp", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("discover error = %v, want context.Canceled", err)
	}
	if !runner.cleaned {
		t.Fatal("scratch configuration was not deleted after cancellation")
	}
	last := runner.calls[len(runner.calls)-1]
	if !slices.Equal(last.Args, []string{"config", "configurations", "delete", "kervik-auth-cancel", "--quiet"}) {
		t.Fatalf("last cleanup call = %+v", last)
	}
}

func TestGCPLoginUsesNonActiveScratchConfigurationAndPreservesADC(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "active_config")
	adcPath := filepath.Join(root, "application_default_credentials.json")
	writeTestFile(t, activePath, "existing")
	writeTestFile(t, adcPath, `{"sentinel":"unchanged"}`)
	runner := &fakeRunner{handler: func(_ string, args, _ []string) ([]byte, error) {
		switch {
		case slices.Equal(args, []string{"config", "get", "core/account", "--configuration=kervik-auth-fixednonce"}):
			return []byte("alice@example.com\n"), nil
		case slices.Equal(args, []string{"projects", "list", "--configuration=kervik-auth-fixednonce", "--filter=lifecycleState:ACTIVE", "--format=json(projectId,name,lifecycleState)"}):
			return []byte(`[{"projectId":"alpha-project","name":"Alpha","lifecycleState":"ACTIVE"}]`), nil
		default:
			return nil, nil
		}
	}}
	manager := newTestManager(t, runner, filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "credentials"), root)
	manager.nonce = func() (string, error) { return "fixednonce", nil }

	response, err := manager.Login(context.Background(), "gcp", LoginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Snapshot.Principal != "alice@example.com" || len(response.Snapshot.Candidates) != 1 {
		t.Fatalf("login response = %+v", response)
	}
	want := [][]string{
		{"config", "configurations", "create", "kervik-auth-fixednonce", "--no-activate", "--quiet"},
		{"auth", "login", "--configuration=kervik-auth-fixednonce", "--brief"},
		{"config", "get", "core/account", "--configuration=kervik-auth-fixednonce"},
		{"projects", "list", "--configuration=kervik-auth-fixednonce", "--filter=lifecycleState:ACTIVE", "--format=json(projectId,name,lifecycleState)"},
		{"config", "configurations", "delete", "kervik-auth-fixednonce", "--quiet"},
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %+v", runner.calls)
	}
	for i, call := range runner.calls {
		if call.Executable != "gcloud" || !slices.Equal(call.Args, want[i]) {
			t.Fatalf("call[%d] = %+v, want %v", i, call, want[i])
		}
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, "--update-adc") || (i == 1 && strings.Contains(joined, "--no-activate")) {
			t.Fatalf("unsafe auth argv: %v", call.Args)
		}
	}
	active, err := os.ReadFile(activePath)
	if err != nil || string(active) != "existing" {
		t.Fatalf("active_config changed: %q, %v", active, err)
	}
	adc, err := os.ReadFile(adcPath)
	if err != nil || string(adc) != `{"sentinel":"unchanged"}` {
		t.Fatalf("ADC changed: %q, %v", adc, err)
	}
}

func TestExecRunnerDoesNotLeakVendorDeviceOutput(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "fakecloud")
	writeTestFile(t, script, "#!/bin/sh\necho 'https://device.example.test CODE-SECRET-123'\necho 'TOKEN-SENTINEL' >&2\nexit 7\n")
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, err := (ExecRunner{}).Run(context.Background(), "fakecloud", []string{"login"}, []string{"PATH=" + bin})
	if err == nil {
		t.Fatal("expected fake CLI failure")
	}
	message := err.Error()
	for _, forbidden := range []string{"device.example.test", "CODE-SECRET-123", "TOKEN-SENTINEL"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("vendor auth output leaked through error: %q", message)
		}
	}
}

func TestExecRunnerRejectsWritableVendorBinaryAndUsesRequestPATH(t *testing.T) {
	unsafeDir := t.TempDir()
	unsafeCLI := filepath.Join(unsafeDir, "fakecloud")
	writeTestFile(t, unsafeCLI, "#!/bin/sh\nprintf unsafe\n")
	if err := os.Chmod(unsafeCLI, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := (ExecRunner{}).Run(
		context.Background(),
		"fakecloud",
		nil,
		[]string{"PATH=" + unsafeDir},
	)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("writable vendor CLI was not rejected: %v", err)
	}

	safeDir := t.TempDir()
	target := filepath.Join(safeDir, "vendor-real")
	writeTestFile(t, target, "#!/bin/sh\nprintf safe\n")
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasDir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(aliasDir, "fakecloud")); err != nil {
		t.Fatal(err)
	}
	out, err := (ExecRunner{}).Run(
		context.Background(),
		"fakecloud",
		nil,
		[]string{"PATH=" + aliasDir},
	)
	if err != nil || string(out) != "safe" {
		t.Fatalf("resolved safe vendor CLI failed: out=%q err=%v", out, err)
	}
}

func TestExecRunnerAllowsOwnedSymlinkFromOwnedGroupWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(t.TempDir(), "trusted")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "fakecloud")
	writeTestFile(t, target, "#!/bin/sh\nprintf safe\n")
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "fakecloud")); err != nil {
		t.Fatal(err)
	}
	out, err := (ExecRunner{}).Run(context.Background(), "fakecloud", nil, []string{"PATH=" + binDir})
	if err != nil || string(out) != "safe" {
		t.Fatalf("owned Homebrew-style symlink was rejected: output=%q err=%v", out, err)
	}
}

func TestExecRunnerRejectsDirectBinaryInGroupWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(binDir, "fakecloud"), "#!/bin/sh\nprintf unsafe\n")
	if _, err := (ExecRunner{}).Run(context.Background(), "fakecloud", nil, []string{"PATH=" + binDir}); err == nil {
		t.Fatal("direct binary in group-writable search directory was accepted")
	}
}

func TestExecRunnerRejectsRenamedTargetFromGroupWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/env", filepath.Join(binDir, "fakecloud")); err != nil {
		t.Fatal(err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), "fakecloud", []string{"-i", "/usr/bin/printf", "unsafe"}, []string{"PATH=" + binDir}); err == nil {
		t.Fatal("renamed trusted target from group-writable search directory was accepted")
	}
}

func TestVendorExecutionEnvironmentDropsDiscoveryPath(t *testing.T) {
	env := vendorExecutionEnvironment([]string{"HOME=/tmp/home", "PATH=/untrusted/bin:/usr/bin"}, "/trusted/aws")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "/untrusted/bin") || !strings.Contains(joined, "PATH=/trusted:/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Fatalf("unexpected execution environment: %s", joined)
	}
}

func TestExecRunnerRejectsWorldWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(binDir, "fakecloud"), "#!/bin/sh\nprintf unsafe\n")
	if err := os.Chmod(binDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), "fakecloud", nil, []string{"PATH=" + binDir}); err == nil {
		t.Fatal("world-writable search directory was accepted")
	}
}

func TestAuthRunnerResolvesInstalledAWSWhenPresent(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/aws"); err != nil {
		t.Skip("AWS CLI is not installed at the standard macOS path")
	}
	got, err := lookPathInEnvironment("aws", []string{"PATH=/usr/local/bin:/usr/bin:/bin"})
	if err != nil || got == "" {
		t.Fatalf("standard AWS CLI installation was rejected: path=%q err=%v", got, err)
	}
}
