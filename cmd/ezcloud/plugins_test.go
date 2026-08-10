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

// ezcloudBinary is built once (TestMain) and exercised as a real subprocess
// by every test in this file — the only way to test main's CLI surface,
// since it talks to os.Stdout/os.Stdin/os.Exit directly rather than through
// injectable seams. This is the first _test.go file in this package; the
// pattern here (build once, exec many times, sandbox EZCLOUD_DATA_DIR and
// EZCLOUD_CONFIG_DIR per-test) is meant to be reused by future cmd/ezcloud
// test files.
var ezcloudBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ezcloud-cli-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	ezcloudBinary = filepath.Join(dir, "ezcloud")
	build := exec.Command("go", "build", "-o", ezcloudBinary, ".")
	build.Dir = mustGetwd()
	if out, err := build.CombinedOutput(); err != nil {
		panic("build ezcloud test binary: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}

// cliEnv sandboxes one test's data and config dirs. NEVER omit
// EZCLOUD_CONFIG_DIR: internal/audit.DefaultPath() reads a var distinct from
// internal/profile.DefaultRoot()'s EZCLOUD_DATA_DIR, and setting only the
// latter would leak plugin-enable/disable audit entries into the real
// ~/.config/ezcloud/audit.log (coder-p1 hit this live; see their report).
type cliEnv struct {
	dataDir   string
	configDir string
}

func newCLIEnv(t *testing.T) cliEnv {
	t.Helper()
	return cliEnv{dataDir: t.TempDir(), configDir: t.TempDir()}
}

// run executes the built ezcloud binary with this sandbox's env and returns
// stdout, stderr, and the process exit code (0 on success).
func (e cliEnv) run(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(ezcloudBinary, args...)
	cmd.Env = append(os.Environ(),
		"EZCLOUD_DATA_DIR="+e.dataDir,
		"EZCLOUD_CONFIG_DIR="+e.configDir,
	)
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

// runJSON is run, but also decodes stdout as JSON into v; it fails the test
// on a non-zero exit or invalid JSON.
func (e cliEnv) runJSON(t *testing.T, v any, args ...string) {
	t.Helper()
	stdout, stderr, code := e.run(t, args...)
	if code != 0 {
		t.Fatalf("run %v: exit %d, stderr: %s", args, code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), v); err != nil {
		t.Fatalf("run %v: decode json: %v\nstdout: %s", args, err, stdout)
	}
}

// migrateDefaultProfileID runs `profile migrate` (fresh install -> one
// Default profile) and returns its id, ready for plugins list/enable/disable.
func (e cliEnv) migrateDefaultProfileID(t *testing.T) string {
	t.Helper()
	e.run(t, "profile", "migrate")
	var list []struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &list, "profile", "list")
	if len(list) != 1 {
		t.Fatalf("expected 1 profile after migrate, got %d", len(list))
	}
	return list[0].ID
}

func TestPluginsListBareRegistry(t *testing.T) {
	e := newCLIEnv(t)
	var descriptors []plugin.Descriptor
	e.runJSON(t, &descriptors, "plugins", "list")

	if len(descriptors) != 3 {
		t.Fatalf("got %d descriptors, want 3: %+v", len(descriptors), descriptors)
	}
	ids := map[string]bool{}
	for _, d := range descriptors {
		ids[d.ID] = true
		if d.Name == "" || d.Icon == "" {
			t.Errorf("%s: missing display metadata: %+v", d.ID, d)
		}
	}
	for _, want := range []string{plugin.CloudAccountsID, plugin.LaunchTemplatesID, plugin.TransferID} {
		if !ids[want] {
			t.Errorf("bare registry missing %q", want)
		}
	}
}

func TestPluginsListPerProfileFreshAllDisabled(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	var out []struct {
		plugin.Descriptor
		Enabled bool `json:"enabled"`
	}
	e.runJSON(t, &out, "plugins", "list", "--profile", id)
	if len(out) != 3 {
		t.Fatalf("got %d entries, want 3", len(out))
	}
	for _, d := range out {
		if d.Enabled {
			t.Errorf("%s: enabled = true on a fresh profile, want false", d.ID)
		}
	}
}

func TestPluginsEnableThenListReflectsState(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	e.run(t, "plugins", "enable", "--profile", id, "--id", plugin.CloudAccountsID)

	var out []struct {
		plugin.Descriptor
		Enabled bool `json:"enabled"`
	}
	e.runJSON(t, &out, "plugins", "list", "--profile", id)
	for _, d := range out {
		want := d.ID == plugin.CloudAccountsID
		if d.Enabled != want {
			t.Errorf("%s: enabled = %v, want %v", d.ID, d.Enabled, want)
		}
	}
}

func TestPluginsEnableIsIdempotent(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	e.run(t, "plugins", "enable", "--profile", id, "--id", plugin.CloudAccountsID)
	e.run(t, "plugins", "enable", "--profile", id, "--id", plugin.CloudAccountsID)

	var p struct {
		EnabledPlugins []string `json:"enabledPlugins"`
	}
	e.runJSON(t, &p, "profile", "show", "--id", id)
	if len(p.EnabledPlugins) != 1 {
		t.Fatalf("enabledPlugins = %+v, want exactly one entry after two enables", p.EnabledPlugins)
	}
}

func TestPluginsDisableOfNotEnabledIsSafe(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	_, stderr, code := e.run(t, "plugins", "disable", "--profile", id, "--id", plugin.TransferID)
	if code != 0 {
		t.Fatalf("disabling a not-enabled plugin should succeed, got exit %d: %s", code, stderr)
	}

	var p struct {
		EnabledPlugins []string `json:"enabledPlugins"`
	}
	e.runJSON(t, &p, "profile", "show", "--id", id)
	if len(p.EnabledPlugins) != 0 {
		t.Fatalf("enabledPlugins = %+v, want still empty", p.EnabledPlugins)
	}
}

func TestPluginsEnableDisableRoundTrip(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	e.run(t, "plugins", "enable", "--profile", id, "--id", plugin.LaunchTemplatesID)
	e.run(t, "plugins", "disable", "--profile", id, "--id", plugin.LaunchTemplatesID)

	var p struct {
		EnabledPlugins []string `json:"enabledPlugins"`
	}
	e.runJSON(t, &p, "profile", "show", "--id", id)
	if len(p.EnabledPlugins) != 0 {
		t.Fatalf("enabledPlugins = %+v, want empty after enable then disable", p.EnabledPlugins)
	}
}

func TestPluginsEnableUnknownIDRejected(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	_, stderr, code := e.run(t, "plugins", "enable", "--profile", id, "--id", "does-not-exist")
	if code == 0 {
		t.Fatal("expected a non-zero exit for an unknown plugin id")
	}
	if stderr == "" {
		t.Fatal("expected an error message on stderr")
	}

	var p struct {
		EnabledPlugins []string `json:"enabledPlugins"`
	}
	e.runJSON(t, &p, "profile", "show", "--id", id)
	if len(p.EnabledPlugins) != 0 {
		t.Fatalf("enabledPlugins = %+v, want unchanged after a rejected enable", p.EnabledPlugins)
	}
}

func TestPluginsEnableMissingFlagsRejected(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	if _, _, code := e.run(t, "plugins", "enable", "--id", plugin.CloudAccountsID); code == 0 {
		t.Fatal("expected failure with --profile omitted")
	}
	if _, _, code := e.run(t, "plugins", "enable", "--profile", id); code == 0 {
		t.Fatal("expected failure with --id omitted")
	}
}

func TestPluginsEnableUnknownProfileRejected(t *testing.T) {
	e := newCLIEnv(t)
	e.migrateDefaultProfileID(t) // seed a data dir, otherwise Get fails for an unrelated reason

	if _, _, code := e.run(t, "plugins", "enable", "--profile", "does-not-exist", "--id", plugin.CloudAccountsID); code == 0 {
		t.Fatal("expected failure for an unknown profile id")
	}
}

func TestPluginsPerProfileIndependenceViaCLI(t *testing.T) {
	e := newCLIEnv(t)
	var a, b struct {
		ID string `json:"id"`
	}
	e.runJSON(t, &a, "profile", "create", "--name", "profile-a")
	e.runJSON(t, &b, "profile", "create", "--name", "profile-b")

	e.run(t, "plugins", "enable", "--profile", a.ID, "--id", plugin.CloudAccountsID)
	e.run(t, "plugins", "enable", "--profile", b.ID, "--id", plugin.LaunchTemplatesID)

	var pa, pb struct {
		EnabledPlugins []string `json:"enabledPlugins"`
	}
	e.runJSON(t, &pa, "profile", "show", "--id", a.ID)
	e.runJSON(t, &pb, "profile", "show", "--id", b.ID)

	if len(pa.EnabledPlugins) != 1 || pa.EnabledPlugins[0] != plugin.CloudAccountsID {
		t.Fatalf("profile a enabledPlugins = %+v", pa.EnabledPlugins)
	}
	if len(pb.EnabledPlugins) != 1 || pb.EnabledPlugins[0] != plugin.LaunchTemplatesID {
		t.Fatalf("profile b enabledPlugins = %+v", pb.EnabledPlugins)
	}
}

func TestPluginsEnableDisableWriteAuditEntries(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	e.run(t, "plugins", "enable", "--profile", id, "--id", plugin.CloudAccountsID)
	e.run(t, "plugins", "disable", "--profile", id, "--id", plugin.CloudAccountsID)

	events, err := audit.List(filepath.Join(e.configDir, "audit.log"), 0)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d audit events, want 2: %+v", len(events), events)
	}
	if events[0].Action != "plugin-enable" || events[1].Action != "plugin-disable" {
		t.Fatalf("actions = %q, %q", events[0].Action, events[1].Action)
	}
	for _, ev := range events {
		if ev.Profile != "Default" {
			t.Errorf("event %+v: profile = %q, want %q", ev, ev.Profile, "Default")
		}
		if len(ev.Keys) != 1 || ev.Keys[0] != plugin.CloudAccountsID {
			t.Errorf("event %+v: keys = %+v, want [%s]", ev, ev.Keys, plugin.CloudAccountsID)
		}
	}
}

func TestPluginsEnableRejectedDoesNotWriteAuditEntry(t *testing.T) {
	e := newCLIEnv(t)
	id := e.migrateDefaultProfileID(t)

	e.run(t, "plugins", "enable", "--profile", id, "--id", "does-not-exist")

	events, err := audit.List(filepath.Join(e.configDir, "audit.log"), 0)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d audit events, want 0 for a rejected enable: %+v", len(events), events)
	}
}

func TestPluginsUpdateAppliesBatchOnceAndPreservesUnknownEnabledIDs(t *testing.T) {
	e := newCLIEnv(t)
	created, err := profilemodel.Create(filepath.Join(e.dataDir, "profiles"), profilemodel.Profile{
		Name:           "batch",
		EnabledPlugins: []string{"some-future-plugin", plugin.CloudAccountsID},
	})
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	body, err := json.Marshal(pluginUpdateRequest{Changes: map[string]bool{
		plugin.CloudAccountsID:   false,
		plugin.LaunchTemplatesID: true,
		plugin.TransferID:        true,
	}})
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := e.runStdin(t, string(body), "plugins", "update", "--profile", created.ID)
	if code != 0 {
		t.Fatalf("update exit %d, stderr: %s", code, stderr)
	}
	var saved profilemodel.Profile
	if err := json.Unmarshal([]byte(stdout), &saved); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout)
	}
	wantEnabled := []string{"some-future-plugin", plugin.LaunchTemplatesID, plugin.TransferID}
	if len(saved.EnabledPlugins) != len(wantEnabled) {
		t.Fatalf("enabledPlugins = %+v, want %+v", saved.EnabledPlugins, wantEnabled)
	}
	for i := range wantEnabled {
		if saved.EnabledPlugins[i] != wantEnabled[i] {
			t.Fatalf("enabledPlugins = %+v, want %+v", saved.EnabledPlugins, wantEnabled)
		}
	}

	events, err := audit.List(filepath.Join(e.configDir, "audit.log"), 0)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want one batch event: %+v", len(events), events)
	}
	if events[0].Action != "plugins-update" || events[0].Profile != "batch" {
		t.Fatalf("batch audit event = %+v", events[0])
	}
	wantKeys := []string{plugin.CloudAccountsID, plugin.LaunchTemplatesID, plugin.TransferID}
	if len(events[0].Keys) != len(wantKeys) {
		t.Fatalf("audit keys = %+v, want %+v", events[0].Keys, wantKeys)
	}
	for i := range wantKeys {
		if events[0].Keys[i] != wantKeys[i] {
			t.Fatalf("audit keys = %+v, want %+v", events[0].Keys, wantKeys)
		}
	}
}

func TestPluginsUpdateValidatesWholeBatchBeforeMutation(t *testing.T) {
	e := newCLIEnv(t)
	created, err := profilemodel.Create(filepath.Join(e.dataDir, "profiles"), profilemodel.Profile{
		Name:           "atomic",
		EnabledPlugins: []string{plugin.TransferID},
	})
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	body, err := json.Marshal(pluginUpdateRequest{Changes: map[string]bool{
		plugin.CloudAccountsID: true,
		"does-not-exist":       true,
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, stderr, code := e.runStdin(t, string(body), "plugins", "update", "--profile", created.ID)
	if code == 0 {
		t.Fatal("expected update with an unknown plugin to fail")
	}
	if !strings.Contains(stderr, "unknown plugin") {
		t.Fatalf("stderr = %q, want unknown-plugin error", stderr)
	}
	persisted, err := profilemodel.Get(filepath.Join(e.dataDir, "profiles"), created.ID)
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if len(persisted.EnabledPlugins) != 1 || persisted.EnabledPlugins[0] != plugin.TransferID {
		t.Fatalf("profile mutated after rejected batch: %+v", persisted.EnabledPlugins)
	}
	events, err := audit.List(filepath.Join(e.configDir, "audit.log"), 0)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got audit events for a rejected batch: %+v", events)
	}
}
