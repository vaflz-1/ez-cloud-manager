package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ez-cloud-manager/internal/audit"
	"ez-cloud-manager/internal/plugin"
	profilemodel "ez-cloud-manager/internal/profile"
)

// runStdin is cliEnv.run but feeding stdin a fixed string — needed for every
// verb that reads a JSON body from stdin (`profile settings set`, `profile
// save`, …). Without an explicit Stdin, the child inherits the test
// process's own stdin, which blocks forever waiting for EOF that never
// comes (see readOptionalCreateRequest/profileSettingsCommand's
// io.ReadAll(io.LimitReader(os.Stdin, …))) — every stdin-reading verb must
// be exercised through this, never cliEnv.run.
func (e cliEnv) runStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(ezcloudBinary, args...)
	cmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
	)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return outBuf.String(), errBuf.String(), code
}

func TestProfileSettingsGetUnknownPluginReturnsEmptyObject(t *testing.T) {
	e := newCLIEnv(t)
	var p struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &p, "profile", "create", "--name", "a")

	stdout, stderr, code := e.run(t, "profile", "settings", "get", "--id", p.ID, "--plugin", "no-such-plugin")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "{}" {
		t.Fatalf("stdout = %q, want {}", stdout)
	}
}

func TestProfileConnectionsAddRemoveAndAuthorize(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")
	if _, stderr, code := e.runStdin(t, `{"fields":{"tenant_id":"tenant"},"expectAbsent":true}`,
		"save", "--provider", "azure", "--workspace", workspace.ID, "--profile", "prod"); code != 0 {
		t.Fatalf("seed Connection: exit %d, stderr: %s", code, stderr)
	}

	var added profilemodel.Profile
	e.runJSON(t, &added,
		"profile", "connections", "add",
		"--id", workspace.ID, "--provider", "azure", "--account", "prod",
	)
	if !profilemodel.AllowsConnection(added, profilemodel.AccountRef{Provider: "azure", Account: "prod"}) {
		t.Fatal("add endpoint did not persist the Connection grant")
	}

	assertAuthorized := func(want bool) {
		t.Helper()
		var response struct {
			Allowed bool `json:"allowed"`
		}
		e.runJSON(t, &response,
			"profile", "connections", "authorize",
			"--id", workspace.ID, "--provider", "azure", "--account", "prod",
		)
		if response.Allowed != want {
			t.Fatalf("authorize = %t, want %t", response.Allowed, want)
		}
	}
	assertAuthorized(true)

	var removed profilemodel.Profile
	e.runJSON(t, &removed,
		"profile", "connections", "remove",
		"--id", workspace.ID, "--provider", "azure", "--account", "prod",
	)
	if profilemodel.AllowsConnection(removed, profilemodel.AccountRef{Provider: "azure", Account: "prod"}) {
		t.Fatal("remove endpoint retained the Connection grant")
	}
	assertAuthorized(false)

	_, stderr, code := e.run(t,
		"profile", "connections", "add",
		"--id", workspace.ID, "--provider", "azure", "--account", "does-not-exist",
	)
	if code == 0 || !strings.Contains(stderr, "does not exist") {
		t.Fatalf("nonexistent Connection grant exit=%d stderr=%q, want rejection", code, stderr)
	}
}

func TestProfileSettingsCloudAccountsRejectsStaleUpdatedAt(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")

	added, err := profilemodel.AddConnectionRef(filepath.Join(e.dataDir, "profiles"), workspace.ID, profilemodel.AccountRef{
		Provider: "aws", Account: "latest",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, code := e.runStdin(t,
		`{"accounts":[{"provider":"aws","account":"stale"}]}`,
		"profile", "settings", "set", "--id", workspace.ID,
		"--plugin", plugin.CloudAccountsID,
		"--expected-updated-at", workspace.UpdatedAt,
	)
	if code == 0 || !strings.Contains(stderr, profilemodel.SettingsConflictMarker) {
		t.Fatalf("stale settings set exit=%d stderr=%q, want stable conflict marker", code, stderr)
	}
	if !profilemodel.AllowsConnection(added, profilemodel.AccountRef{Provider: "aws", Account: "latest"}) {
		t.Fatal("test setup did not add latest ref")
	}

	var current profilemodel.Profile
	e.runJSON(t, &current, "profile", "show", "--id", workspace.ID)
	if !profilemodel.AllowsConnection(current, profilemodel.AccountRef{Provider: "aws", Account: "latest"}) ||
		profilemodel.AllowsConnection(current, profilemodel.AccountRef{Provider: "aws", Account: "stale"}) {
		t.Fatalf("stale full settings write replaced latest policy: %+v", current)
	}
}

func TestProfileSettingsSetGetRoundTrip(t *testing.T) {
	e := newCLIEnv(t)
	var p profilemodel.Profile
	e.runJSON(t, &p, "profile", "create", "--name", "a")
	seedWorkspaceAzureConnection(t, e, p.ID, "prod")

	stdout, stderr, code := e.runStdin(t, `{"showAllAccounts":false,"accounts":[{"provider":"azure","account":"prod"}]}`,
		"profile", "settings", "set", "--id", p.ID, "--plugin", plugin.CloudAccountsID,
		"--expected-updated-at", p.UpdatedAt)
	if code != 0 {
		t.Fatalf("set exit %d, stderr: %s", code, stderr)
	}
	var saved struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal([]byte(stdout), &saved); err != nil {
		t.Fatalf("decode set response: %v\n%s", err, stdout)
	}
	if _, ok := saved.Settings[plugin.CloudAccountsID]; !ok {
		t.Fatalf("set response missing settings.%s: %s", plugin.CloudAccountsID, stdout)
	}

	getOut, stderr, code := e.run(t, "profile", "settings", "get", "--id", p.ID, "--plugin", plugin.CloudAccountsID)
	if code != 0 {
		t.Fatalf("get exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		ShowAllAccounts bool `json:"showAllAccounts"`
		Accounts        []struct {
			Provider string `json:"provider"`
			Account  string `json:"account"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(getOut), &got); err != nil {
		t.Fatalf("decode get: %v\n%s", err, getOut)
	}
	if len(got.Accounts) != 1 || got.Accounts[0].Provider != "azure" || got.Accounts[0].Account != "prod" {
		t.Fatalf("round-tripped accounts = %+v", got.Accounts)
	}
}

func TestProfileSettingsSetInvalidJSONRejected(t *testing.T) {
	e := newCLIEnv(t)
	var p struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &p, "profile", "create", "--name", "a")

	_, stderr, code := e.runStdin(t, "not-json-at-all", "profile", "settings", "set", "--id", p.ID, "--plugin", plugin.CloudAccountsID)
	if code == 0 {
		t.Fatalf("expected rejection of an invalid JSON blob, stderr was: %s", stderr)
	}
}

func TestProfileSettingsSetCloudAccountsValidatesAccounts(t *testing.T) {
	e := newCLIEnv(t)
	var p struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &p, "profile", "create", "--name", "a")

	cases := []struct {
		desc string
		blob string
	}{
		{"empty provider", `{"accounts":[{"provider":"","account":"x"}]}`},
		{"empty account", `{"accounts":[{"provider":"aws","account":""}]}`},
		{"control char in account", `{"accounts":[{"provider":"aws","account":"bad\naccount"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			_, stderr, code := e.runStdin(t, tc.blob, "profile", "settings", "set", "--id", p.ID, "--plugin", plugin.CloudAccountsID)
			if code == 0 {
				t.Fatalf("expected rejection for %s, stderr was: %s", tc.desc, stderr)
			}
		})
	}
}

func TestProfileSettingsSetRejectsFutureConnectionGrant(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "future-grant")

	_, stderr, code := e.runStdin(t,
		`{"accounts":[{"provider":"azure","account":"not-created-yet"}]}`,
		"profile", "settings", "set", "--id", workspace.ID,
		"--plugin", plugin.CloudAccountsID,
		"--expected-updated-at", workspace.UpdatedAt,
	)
	if code == 0 || !strings.Contains(stderr, "refusing future Connection grant") {
		t.Fatalf("future-name grant exit=%d stderr=%q, want fail-closed rejection", code, stderr)
	}
	var current profilemodel.Profile
	e.runJSON(t, &current, "profile", "show", "--id", workspace.ID)
	if profilemodel.AllowsConnection(current, profilemodel.AccountRef{Provider: "azure", Account: "not-created-yet"}) {
		t.Fatal("rejected future Connection name was still persisted")
	}
}

func TestProfileSettingsSetCloudAccountsDedupes(t *testing.T) {
	e := newCLIEnv(t)
	var p profilemodel.Profile
	e.runJSON(t, &p, "profile", "create", "--name", "a")
	seedWorkspaceAzureConnection(t, e, p.ID, "x")

	stdout, stderr, code := e.runStdin(t, `{"accounts":[{"provider":"azure","account":"x"},{"provider":"azure","account":"x"}]}`,
		"profile", "settings", "set", "--id", p.ID, "--plugin", plugin.CloudAccountsID,
		"--expected-updated-at", p.UpdatedAt)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	_ = stdout

	getOut, _, _ := e.run(t, "profile", "settings", "get", "--id", p.ID, "--plugin", plugin.CloudAccountsID)
	var got struct {
		Accounts []struct{ Provider, Account string } `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(getOut), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, getOut)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("accounts not deduped: %+v", got.Accounts)
	}
}

func TestProfileSettingsSetOversizedOpaqueBlobRejected(t *testing.T) {
	e := newCLIEnv(t)
	var p struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &p, "profile", "create", "--name", "a")

	huge := `{"data":"` + strings.Repeat("a", 17000) + `"}`
	_, stderr, code := e.runStdin(t, huge, "profile", "settings", "set", "--id", p.ID, "--plugin", "some-other-plugin")
	if code == 0 {
		t.Fatalf("expected rejection of an oversized blob, stderr was: %s", stderr)
	}
}

func TestProfileSettingsSetMissingPluginFlagRejected(t *testing.T) {
	e := newCLIEnv(t)
	var p struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &p, "profile", "create", "--name", "a")

	_, _, code := e.runStdin(t, "{}", "profile", "settings", "set", "--id", p.ID, "--plugin", "")
	if code == 0 {
		t.Fatal("expected rejection when --plugin is empty")
	}
}

func TestProfileSettingsSetUnknownProfileRejected(t *testing.T) {
	e := newCLIEnv(t)
	_, _, code := e.runStdin(t, "{}", "profile", "settings", "set", "--id", "does-not-exist", "--plugin", plugin.CloudAccountsID)
	if code == 0 {
		t.Fatal("expected rejection for an unknown profile id")
	}
}

// TestProfileSettingsSetAuditEntryNeverIncludesBlobContents: only the plugin
// id may appear in the audit log, never the settings values themselves (an
// account name, a future plugin's arbitrary blob, etc. could be sensitive).
func TestProfileSettingsSetAuditEntryNeverIncludesBlobContents(t *testing.T) {
	e := newCLIEnv(t)
	var p profilemodel.Profile
	e.runJSON(t, &p, "profile", "create", "--name", "a")
	seedWorkspaceAzureConnection(t, e, p.ID, "super-secret-account-name")
	if err := os.Remove(filepath.Join(e.configDir, "audit.log")); err != nil {
		t.Fatalf("reset audit after Connection fixture: %v", err)
	}

	e.runStdin(t, `{"accounts":[{"provider":"azure","account":"super-secret-account-name"}]}`,
		"profile", "settings", "set", "--id", p.ID, "--plugin", plugin.CloudAccountsID,
		"--expected-updated-at", p.UpdatedAt)

	// profile create also logs an entry, so find the settings-save one
	// specifically rather than assuming it's the only event.
	events, err := audit.List(filepath.Join(e.configDir, "audit.log"), 0)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var settingsEvent *audit.Event
	for i := range events {
		if events[i].Action == "plugin-settings-save" {
			settingsEvent = &events[i]
		}
	}
	if settingsEvent == nil {
		t.Fatalf("no plugin-settings-save event found: %+v", events)
	}
	if len(settingsEvent.Keys) != 1 || settingsEvent.Keys[0] != plugin.CloudAccountsID {
		t.Fatalf("keys = %+v, want [%s]", settingsEvent.Keys, plugin.CloudAccountsID)
	}

	raw, err := os.ReadFile(filepath.Join(e.configDir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-account-name") {
		t.Fatalf("audit.log leaked settings blob content: %s", raw)
	}
}

func seedWorkspaceAzureConnection(t *testing.T, e cliEnv, workspaceID, name string) {
	t.Helper()
	payload, err := json.Marshal(saveRequest{
		Fields:       map[string]string{"tenant_id": "tenant-test-only"},
		ExpectAbsent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.runStdin(t, string(payload),
		"save", "--provider", "azure", "--workspace", workspaceID, "--profile", name,
	); code != 0 {
		t.Fatalf("seed Azure Connection %q: exit %d, stderr: %s", name, code, stderr)
	}
}

func TestProfileSaveUpdatesOnlyCoreFields(t *testing.T) {
	e := newCLIEnv(t)
	root := filepath.Join(e.dataDir, "profiles")
	created, err := profilemodel.Create(root, profilemodel.Profile{
		Name:           "before",
		EnvVars:        []profilemodel.EnvVar{{Key: "REGION", Value: "us-east-1"}},
		EnabledPlugins: []string{"some-future-plugin", plugin.TransferID},
		Settings: map[string]json.RawMessage{
			"some-future-plugin": json.RawMessage(`{"mode":"safe"}`),
		},
		WindowState: json.RawMessage(`{"selected":"catalog"}`),
	})
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	// Send the old whole-object shape with deliberately stale plugin-owned
	// fields. The CLI must accept it for compatibility but ignore those
	// fields, applying only id/name/envVars.
	stale := created
	stale.Name = "after"
	stale.EnvVars = []profilemodel.EnvVar{{Key: "REGION", Value: "eu-west-1"}}
	stale.EnabledPlugins = []string{}
	stale.Settings = map[string]json.RawMessage{"replacement": json.RawMessage(`{"bad":true}`)}
	stale.WindowState = json.RawMessage(`{"selected":"wrong"}`)
	body, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := e.runStdin(t, string(body), "profile", "save")
	if code != 0 {
		t.Fatalf("save exit %d, stderr: %s", code, stderr)
	}
	var response profilemodel.Profile
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode save response: %v\n%s", err, stdout)
	}
	persisted, err := profilemodel.Get(root, created.ID)
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}

	for label, got := range map[string]profilemodel.Profile{"response": response, "persisted": persisted} {
		if got.Name != "after" || len(got.EnvVars) != 1 || got.EnvVars[0].Value != "eu-west-1" {
			t.Errorf("%s core fields = name %q, envVars %+v", label, got.Name, got.EnvVars)
		}
		if len(got.EnabledPlugins) != 2 || got.EnabledPlugins[0] != "some-future-plugin" || got.EnabledPlugins[1] != plugin.TransferID {
			t.Errorf("%s enabledPlugins were clobbered: %+v", label, got.EnabledPlugins)
		}
		var settings struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(got.Settings["some-future-plugin"], &settings); err != nil || settings.Mode != "safe" {
			t.Errorf("%s settings were clobbered: %s (%v)", label, got.Settings["some-future-plugin"], err)
		}
		if _, exists := got.Settings["replacement"]; exists {
			t.Errorf("%s accepted stale replacement settings: %+v", label, got.Settings)
		}
		var state struct {
			Selected string `json:"selected"`
		}
		if err := json.Unmarshal(got.WindowState, &state); err != nil || state.Selected != "catalog" {
			t.Errorf("%s window state was clobbered: %s (%v)", label, got.WindowState, err)
		}
	}
}

func TestProfileSaveRejectsStaleCoreSnapshot(t *testing.T) {
	e := newCLIEnv(t)
	root := filepath.Join(e.dataDir, "profiles")
	created, err := profilemodel.Create(root, profilemodel.Profile{
		Name:    "baseline",
		EnvVars: []profilemodel.EnvVar{{Key: "REGION", Value: "us-east-1"}},
	})
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	fresh, err := profilemodel.UpdateCore(root, profilemodel.CoreUpdate{
		ID:              created.ID,
		Name:            "newer editor",
		EnvVars:         []profilemodel.EnvVar{{Key: "REGION", Value: "eu-west-1"}},
		ExpectedName:    created.Name,
		ExpectedEnvVars: created.EnvVars,
	})
	if err != nil {
		t.Fatalf("seed newer core state: %v", err)
	}

	body, err := json.Marshal(profileSaveRequest{
		ID:              created.ID,
		Name:            "stale editor",
		EnvVars:         []profilemodel.EnvVar{{Key: "REGION", Value: "ap-southeast-1"}},
		ExpectedName:    created.Name,
		ExpectedEnvVars: created.EnvVars,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, code := e.runStdin(t, string(body), "profile", "save")
	if code == 0 {
		t.Fatal("stale profile save unexpectedly succeeded")
	}
	if !strings.Contains(stderr, profilemodel.ErrCoreConflict.Error()) {
		t.Fatalf("stderr = %q, want core conflict", stderr)
	}

	persisted, err := profilemodel.Get(root, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Name != fresh.Name ||
		len(persisted.EnvVars) != 1 ||
		persisted.EnvVars[0].Value != fresh.EnvVars[0].Value {
		t.Fatalf("stale save replaced fresh core: %+v", persisted)
	}
}
