package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	profilemodel "ez-cloud-manager/internal/profile"
	"ez-cloud-manager/internal/provider"
)

func TestSaveWireContractRejectsStaleAndDuplicateCreate(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")
	runSave := func(name string, request saveRequest) (string, int) {
		t.Helper()
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(ezcloudBinary, "save", "--provider", "azure", "--workspace", workspace.ID, "--profile", name)
		cmd.Env = append(os.Environ(),
			"EZCLOUD_DATA_DIR="+e.dataDir,
			"EZCLOUD_CONFIG_DIR="+e.configDir,
		)
		cmd.Stdin = bytes.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode()
		}
		if err != nil {
			t.Fatalf("run save: %v", err)
		}
		return string(out), 0
	}

	original := map[string]string{"tenant_id": "tenant-stable", "client_secret": "secret-original"}
	if out, code := runSave("shared", saveRequest{Fields: original, ExpectAbsent: true}); code != 0 {
		t.Fatalf("initial conditional create: exit %d: %s", code, out)
	}
	var granted profilemodel.Profile
	e.runJSON(t, &granted,
		"profile", "connections", "add", "--id", workspace.ID,
		"--provider", "azure", "--account", "shared",
	)
	updated := map[string]string{"tenant_id": "tenant-stable", "client_secret": "secret-updated"}
	if out, code := runSave("shared", saveRequest{Fields: updated, ExpectedFields: original}); code != 0 {
		t.Fatalf("conditional update: exit %d: %s", code, out)
	}

	out, code := runSave("shared", saveRequest{
		Fields:         map[string]string{"tenant_id": "tenant-stable", "client_secret": "stale-overwrite"},
		ExpectedFields: original,
	})
	if code == 0 || !strings.Contains(out, provider.ConnectionConflictMarker) {
		t.Fatalf("stale update exit=%d output=%q, want stable conflict marker", code, out)
	}
	if out, code := runSave("shared", saveRequest{Fields: original, ExpectAbsent: true}); code == 0 || !strings.Contains(out, provider.ConnectionConflictMarker) {
		t.Fatalf("duplicate create exit=%d output=%q, want stable conflict marker", code, out)
	}

	var got provider.Profile
	e.runJSON(t, &got, "get", "--provider", "azure", "--workspace", workspace.ID, "--profile", "shared")
	if got.Fields["tenant_id"] != "tenant-stable" || got.Fields["client_secret"] != "secret-updated" {
		t.Fatalf("stale save overwrote current fields: %+v", got.Fields)
	}
}

func TestDuplicateConditionalCreateDoesNotDeleteLiveWorkspaceGrant(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")

	request := saveRequest{Fields: map[string]string{"tenant_id": "tenant"}, ExpectAbsent: true}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.runStdin(t, string(payload), "save", "--provider", "azure", "--workspace", workspace.ID, "--profile", "shared"); code != 0 {
		t.Fatalf("initial create: exit %d, stderr: %s", code, stderr)
	}
	var ignored profilemodel.Profile
	e.runJSON(t, &ignored,
		"profile", "connections", "add", "--id", workspace.ID,
		"--provider", "azure", "--account", "shared",
	)

	if _, stderr, code := e.runStdin(t, string(payload), "save", "--provider", "azure", "--workspace", workspace.ID, "--profile", "shared"); code == 0 || !strings.Contains(stderr, provider.ConnectionConflictMarker) {
		t.Fatalf("duplicate create exit=%d stderr=%q, want conflict", code, stderr)
	}
	var authorization struct {
		Allowed bool `json:"allowed"`
	}
	e.runJSON(t, &authorization,
		"profile", "connections", "authorize", "--id", workspace.ID,
		"--provider", "azure", "--account", "shared",
	)
	if !authorization.Allowed {
		t.Fatal("duplicate create conflict removed the live Connection grant")
	}
}

func TestDeleteAndRecreateNeverInheritWorkspaceGrants(t *testing.T) {
	e := newCLIEnv(t)
	var workspaces [2]profilemodel.Profile
	for i, name := range []string{"workspace-a", "workspace-b"} {
		e.runJSON(t, &workspaces[i], "profile", "create", "--name", name)
	}

	runSave := func(expectAbsent bool) {
		t.Helper()
		request := saveRequest{Fields: map[string]string{"tenant_id": "tenant"}, ExpectAbsent: expectAbsent}
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		_, stderr, code := e.runStdin(t, string(payload), "save", "--provider", "azure", "--workspace", workspaces[0].ID, "--profile", "shared")
		if code != 0 {
			t.Fatalf("save expectAbsent=%t: exit %d, stderr: %s", expectAbsent, code, stderr)
		}
	}
	addGrant := func(workspaceID string, requireExisting bool) {
		t.Helper()
		if !requireExisting {
			if _, err := profilemodel.AddConnectionRef(filepath.Join(e.dataDir, "profiles"), workspaceID, profilemodel.AccountRef{
				Provider: "azure", Account: "shared",
			}); err != nil {
				t.Fatal(err)
			}
			return
		}
		var ignored profilemodel.Profile
		e.runJSON(t, &ignored,
			"profile", "connections", "add", "--id", workspaceID,
			"--provider", "azure", "--account", "shared",
		)
	}
	assertDeniedEverywhere := func() {
		t.Helper()
		for _, workspace := range workspaces {
			var response struct {
				Allowed bool `json:"allowed"`
			}
			e.runJSON(t, &response,
				"profile", "connections", "authorize", "--id", workspace.ID,
				"--provider", "azure", "--account", "shared",
			)
			if response.Allowed {
				t.Fatalf("workspace %q retained/resurrected a grant", workspace.Name)
			}
		}
	}

	runSave(true)
	for _, workspace := range workspaces {
		addGrant(workspace.ID, true)
	}
	if _, stderr, code := e.run(t, "delete", "--provider", "azure", "--workspace", workspaces[0].ID, "--profile", "shared"); code != 0 {
		t.Fatalf("delete: exit %d, stderr: %s", code, stderr)
	}
	assertDeniedEverywhere()

	// Simulate dangling refs left by an old build, then cover unconditional
	// creation of the missing provider record.
	for _, workspace := range workspaces {
		addGrant(workspace.ID, false)
	}
	runSave(false)
	assertDeniedEverywhere()

	// Re-authorization is explicit after a replacement identity is created.
	// Without this grant the hardened delete path must reject the operation.
	addGrant(workspaces[0].ID, true)
	if _, stderr, code := e.run(t, "delete", "--provider", "azure", "--workspace", workspaces[0].ID, "--profile", "shared"); code != 0 {
		t.Fatalf("second delete: exit %d, stderr: %s", code, stderr)
	}
	for _, workspace := range workspaces {
		addGrant(workspace.ID, false)
	}
	runSave(true)
	assertDeniedEverywhere()
}

func TestDeleteCleansOnlyWorkspacesUsingTheMatchingProviderStore(t *testing.T) {
	e := newCLIEnv(t)
	storeA := filepath.Join(e.configDir, "aws-a")
	storeB := filepath.Join(e.configDir, "aws-b")
	createWorkspace := func(name, store string) profilemodel.Profile {
		t.Helper()
		created, err := profilemodel.Create(filepath.Join(e.dataDir, "profiles"), profilemodel.Profile{
			Name: name,
			EnvVars: []profilemodel.EnvVar{
				{Key: "AWS_SHARED_CREDENTIALS_FILE", Value: store},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	workspaceA := createWorkspace("store-a", storeA)
	workspaceB := createWorkspace("store-b", storeB)

	backend, err := provider.Get("aws")
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []string{storeA, storeB} {
		if err := backend.Save(store, "shared", map[string]string{"aws_access_key_id": "AKIAEXAMPLE"}); err != nil {
			t.Fatalf("seed %q: %v", store, err)
		}
	}
	for _, workspace := range []profilemodel.Profile{workspaceA, workspaceB} {
		var ignored profilemodel.Profile
		e.runJSON(t, &ignored,
			"profile", "connections", "add", "--id", workspace.ID,
			"--provider", "aws", "--account", "shared",
		)
	}

	deleteCmd := exec.Command(ezcloudBinary, "delete", "--provider", "aws", "--workspace", workspaceA.ID, "--profile", "shared")
	deleteCmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
		"AWS_SHARED_CREDENTIALS_FILE="+storeA,
	)
	if out, err := deleteCmd.CombinedOutput(); err != nil {
		t.Fatalf("delete store A: %v: %s", err, out)
	}
	assertAuthorization := func(workspace profilemodel.Profile, want bool) {
		t.Helper()
		var response struct {
			Allowed bool `json:"allowed"`
		}
		e.runJSON(t, &response,
			"profile", "connections", "authorize", "--id", workspace.ID,
			"--provider", "aws", "--account", "shared",
		)
		if response.Allowed != want {
			t.Fatalf("workspace %q allowed=%t, want %t", workspace.Name, response.Allowed, want)
		}
	}
	assertAuthorization(workspaceA, false)
	assertAuthorization(workspaceB, true)

	profilesB, err := backend.List(storeB)
	if err != nil {
		t.Fatal(err)
	}
	if len(profilesB) != 1 || profilesB[0].Name != "shared" {
		t.Fatalf("delete from store A mutated store B: %+v", profilesB)
	}
}

func TestCanonicalConnectionStorePathResolvesLongestExistingSymlinkAncestor(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	throughLink := filepath.Join(linkRoot, "missing", "credentials")
	direct := filepath.Join(realRoot, "missing", "credentials")
	gotLink, err := canonicalConnectionStorePath(throughLink)
	if err != nil {
		t.Fatal(err)
	}
	gotDirect, err := canonicalConnectionStorePath(direct)
	if err != nil {
		t.Fatal(err)
	}
	if gotLink != gotDirect {
		t.Fatalf("symlinked identities differ: %q != %q", gotLink, gotDirect)
	}
}

func TestDeleteReportsStablePartialSuccessWhenScopeCleanupFails(t *testing.T) {
	e := newCLIEnv(t)
	var authorized, broken profilemodel.Profile
	e.runJSON(t, &authorized, "profile", "create", "--name", "authorized")
	e.runJSON(t, &broken, "profile", "create", "--name", "zz-broken")
	request := saveRequest{Fields: map[string]string{"tenant_id": "tenant"}, ExpectAbsent: true}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.runStdin(t, string(payload), "save", "--provider", "azure", "--workspace", authorized.ID, "--profile", "partial"); code != 0 {
		t.Fatalf("create: exit %d, stderr: %s", code, stderr)
	}
	for _, workspace := range []profilemodel.Profile{authorized, broken} {
		var ignored profilemodel.Profile
		e.runJSON(t, &ignored,
			"profile", "connections", "add", "--id", workspace.ID,
			"--provider", "azure", "--account", "partial",
		)
	}

	// The operation can authenticate through `authorized`, then fail while
	// atomically rewriting a different matching Workspace. This exercises the
	// stable "provider record deleted, policy cleanup incomplete" contract
	// without bypassing the new mandatory Workspace authorization boundary.
	brokenDir := filepath.Join(e.dataDir, "profiles", broken.ID)
	if err := os.Chmod(brokenDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(brokenDir, 0o700) })

	cmd := exec.Command(ezcloudBinary, "delete", "--provider", "azure", "--workspace", authorized.ID, "--profile", "partial")
	cmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
	)
	out, runErr := cmd.CombinedOutput()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok || exitErr.ExitCode() == 0 || !strings.Contains(string(out), connectionDeletedCleanupFailedMarker) {
		t.Fatalf("partial delete error=%v output=%q, want stable cleanup marker", runErr, out)
	}

	var listed listResponse
	e.runJSON(t, &listed, "list", "--provider", "azure")
	for _, candidate := range listed.Profiles {
		if candidate.Name == "partial" {
			t.Fatalf("provider record still exists after partial delete: %+v", candidate)
		}
	}
}

func TestProviderOperationsRejectUnscopedAndReplacementIdentityRead(t *testing.T) {
	e := newCLIEnv(t)
	var workspace profilemodel.Profile
	e.runJSON(t, &workspace, "profile", "create", "--name", "workspace")

	create := func(tenant string) {
		t.Helper()
		payload, err := json.Marshal(saveRequest{
			Fields:       map[string]string{"tenant_id": tenant},
			ExpectAbsent: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := e.runStdin(t, string(payload),
			"save", "--provider", "azure", "--workspace", workspace.ID, "--profile", "shared",
		); code != 0 {
			t.Fatalf("create replacement %q: exit %d, stderr: %s", tenant, code, stderr)
		}
	}

	create("identity-a")
	var ignored profilemodel.Profile
	e.runJSON(t, &ignored,
		"profile", "connections", "add", "--id", workspace.ID,
		"--provider", "azure", "--account", "shared",
	)

	if _, stderr, code := e.run(t, "get", "--provider", "azure", "--profile", "shared"); code == 0 || !strings.Contains(stderr, "--workspace is required") {
		t.Fatalf("unscoped get exit=%d stderr=%q, want mandatory Workspace rejection", code, stderr)
	}
	if _, stderr, code := e.run(t,
		"delete", "--provider", "azure", "--workspace", workspace.ID, "--profile", "shared",
	); code != 0 {
		t.Fatalf("delete original identity: exit %d, stderr: %s", code, stderr)
	}

	create("identity-b")
	if _, stderr, code := e.run(t,
		"get", "--provider", "azure", "--workspace", workspace.ID, "--profile", "shared",
	); code == 0 || !strings.Contains(stderr, "not allowed") {
		t.Fatalf("replacement read exit=%d stderr=%q, want fresh Workspace authorization", code, stderr)
	}
}

func TestSensitiveProviderOperationsRequireWorkspace(t *testing.T) {
	e := newCLIEnv(t)
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{name: "get", args: []string{"get", "--provider", "azure", "--profile", "shared"}},
		{
			name:  "save",
			stdin: `{"fields":{"tenant_id":"tenant"},"expectAbsent":true}`,
			args:  []string{"save", "--provider", "azure", "--profile", "shared"},
		},
		{name: "delete", args: []string{"delete", "--provider", "azure", "--profile", "shared"}},
		{name: "export", args: []string{"export", "--provider", "azure", "--profile", "shared", "--format", "json"}},
		{name: "activate", args: []string{"activate", "--provider", "azure", "--profile", "shared"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr string
			var code int
			if tc.stdin == "" {
				_, stderr, code = e.run(t, tc.args...)
			} else {
				_, stderr, code = e.runStdin(t, tc.stdin, tc.args...)
			}
			if code == 0 || !strings.Contains(stderr, "--workspace is required") {
				t.Fatalf("exit=%d stderr=%q, want mandatory Workspace rejection", code, stderr)
			}
		})
	}
}

func TestExistingConnectionMaterialIdentityChangeRequiresNewName(t *testing.T) {
	e := newCLIEnv(t)
	var workspaces [2]profilemodel.Profile
	for i, name := range []string{"identity-owner", "identity-consumer"} {
		e.runJSON(t, &workspaces[i], "profile", "create", "--name", name)
	}
	create := saveRequest{
		Fields: map[string]string{
			"tenant_id":     "tenant-original",
			"client_id":     "client-original",
			"client_secret": "secret-original",
		},
		ExpectAbsent: true,
	}
	payload, _ := json.Marshal(create)
	if _, stderr, code := e.runStdin(t, string(payload),
		"save", "--provider", "azure", "--workspace", workspaces[0].ID, "--profile", "shared",
	); code != 0 {
		t.Fatalf("create: exit %d, stderr: %s", code, stderr)
	}
	for _, workspace := range workspaces {
		var ignored profilemodel.Profile
		e.runJSON(t, &ignored,
			"profile", "connections", "add", "--id", workspace.ID,
			"--provider", "azure", "--account", "shared",
		)
	}
	update := saveRequest{
		Fields: map[string]string{
			"tenant_id":     "tenant-replacement",
			"client_id":     "client-replacement",
			"client_secret": "secret-new",
		},
		ExpectedFields: create.Fields,
	}
	payload, _ = json.Marshal(update)
	_, stderr, code := e.runStdin(t, string(payload),
		"save", "--provider", "azure", "--workspace", workspaces[0].ID, "--profile", "shared",
	)
	if code == 0 || !strings.Contains(stderr, connectionIdentityChangeMarker) {
		t.Fatalf("identity replacement exit=%d stderr=%q, want stable rejection", code, stderr)
	}
	var got provider.Profile
	e.runJSON(t, &got,
		"get", "--provider", "azure", "--workspace", workspaces[1].ID, "--profile", "shared",
	)
	if got.Fields["tenant_id"] != "tenant-original" || got.Fields["client_id"] != "client-original" {
		t.Fatalf("rejected identity replacement changed shared Connection: %+v", got.Fields)
	}
}
