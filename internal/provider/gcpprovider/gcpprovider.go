// Package gcpprovider adapts gcloud named configurations
// (internal/gcpcreds) to the generic provider.Provider interface and
// registers it as "gcp".
//
// Import for side effects to make the provider available:
//
//	import _ "ez-cloud-manager/internal/provider/gcpprovider"
package gcpprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"ez-cloud-manager/internal/gcpcreds"
	"ez-cloud-manager/internal/provider"
)

const id = "gcp"

type gcpProvider struct{}

// New returns the Google Cloud provider, backed by gcloud's own
// configurations directory.
func New() provider.Provider { return gcpProvider{} }

func (gcpProvider) ID() string          { return id }
func (gcpProvider) DisplayName() string { return "Google Cloud" }

func (gcpProvider) DefaultPath() (string, error) { return gcpcreds.DefaultPath() }

func (gcpProvider) List(path string) ([]provider.ProfileSummary, error) {
	summaries, err := gcpcreds.List(path)
	if err != nil {
		return nil, err
	}
	active := gcpcreds.ActiveName(path)
	out := make([]provider.ProfileSummary, len(summaries))
	for i, s := range summaries {
		out[i] = provider.ProfileSummary{Name: s.Name, Keys: s.Keys, Active: s.Name == active}
	}
	return out, nil
}

func (gcpProvider) Get(path, name string) (provider.Profile, error) {
	p, err := gcpcreds.Get(path, name)
	if err != nil {
		return provider.Profile{}, err
	}
	return provider.Profile{Name: p.Name, Fields: p.Fields}, nil
}

func (gcpProvider) Save(path, name string, fields map[string]string) error {
	return gcpcreds.Save(path, name, fields)
}

func (gcpProvider) Delete(path, name string) error {
	return gcpcreds.Delete(path, name)
}

func (gcpProvider) Parse(text string) provider.Parsed {
	p := gcpcreds.Parse(text)
	return provider.Parsed{ProfileName: p.ProfileName, Fields: p.Fields, Notes: p.Notes}
}

// Schema exports gcloud properties with their CLOUDSDK_* env spellings (the
// systematic form gcloud itself honors); GOOGLE_* aliases are accepted on
// import. No gcloud property is secret material — key files stay on disk.
func (gcpProvider) Schema() provider.Schema {
	return provider.Schema{
		Provider:    id,
		DisplayName: "Google Cloud",
		Fields: []provider.FieldSpec{
			{Key: gcpcreds.KeyAccount, Display: "core/account", Env: "CLOUDSDK_CORE_ACCOUNT", Common: true, Placeholder: "you@example.com"},
			{Key: gcpcreds.KeyProject, Display: "core/project", Env: "CLOUDSDK_CORE_PROJECT", Common: true, Placeholder: "my-project-id"},
			{Key: gcpcreds.KeyRegion, Display: "compute/region", Env: "CLOUDSDK_COMPUTE_REGION", Common: true, Placeholder: "us-central1"},
			{Key: gcpcreds.KeyZone, Display: "compute/zone", Env: "CLOUDSDK_COMPUTE_ZONE", Common: true, Placeholder: "us-central1-a"},
			{Key: gcpcreds.KeyCredFile, Display: "GOOGLE_APPLICATION_CREDENTIALS", Env: "GOOGLE_APPLICATION_CREDENTIALS", Placeholder: "/path/to/service-account.json"},
			{Key: gcpcreds.KeyUsageReport, Display: "core/disable_usage_reporting", Env: "CLOUDSDK_CORE_DISABLE_USAGE_REPORTING", Placeholder: "true"},
		},
	}
}

// Activate writes gcloud's active_config marker — the same effect as
// `gcloud config configurations activate NAME`.
func (gcpProvider) Activate(path, name string) error {
	return gcpcreds.Activate(path, name)
}

func (gcpProvider) ActivateLabel() string { return "Set as active gcloud configuration" }

// Check runs `gcloud auth list` scoped to the named configuration via
// CLOUDSDK_ACTIVE_CONFIG_NAME — gcloud's documented one-shot override for
// which configuration a single invocation uses, WITHOUT touching the
// on-disk active_config marker (unlike Activate). path is the CLOUDSDK_CONFIG
// root (see gcpcreds.DefaultPath). A missing gcloud binary or a failed call
// is reported via CheckResult, never a Go error.
func (gcpProvider) Check(ctx context.Context, path, name string) (provider.CheckResult, error) {
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		return provider.CheckResult{OK: false, Error: "gcloud CLI not found in PATH"}, nil
	}

	cmd := exec.CommandContext(ctx, bin, "auth", "list", "--format=json")
	cmd.Env = append(os.Environ(), "CLOUDSDK_CONFIG="+path, "CLOUDSDK_ACTIVE_CONFIG_NAME="+name)
	// See awsprovider.Check's identical field for why this is required, not
	// optional: gcloud (or a wrapper script around it) spawning a child of
	// its own means killing gcloud alone on ctx expiry can still leave
	// cmd.Wait blocked on that orphaned child holding the output pipes open.
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() == context.DeadlineExceeded {
			msg = "timed out waiting for gcloud auth list"
		} else if msg == "" {
			msg = err.Error()
		}
		return provider.CheckResult{OK: false, Error: msg}, nil
	}

	var accounts []struct {
		Account string
		Status  string
	}
	_ = json.Unmarshal(stdout.Bytes(), &accounts)
	for _, a := range accounts {
		if a.Status == "ACTIVE" {
			identity := map[string]string{"account": a.Account}
			// The project id comes from the STORED configuration's own
			// fields, not a second live gcloud call — `gcloud auth list`
			// doesn't carry it, and this configuration's file is already
			// ours to read directly.
			if stored, err := gcpcreds.Get(path, name); err == nil {
				if proj := stored.Fields[gcpcreds.KeyProject]; proj != "" {
					identity["project"] = proj
				}
			}
			return provider.CheckResult{OK: true, Identity: identity}, nil
		}
	}
	return provider.CheckResult{OK: false, Error: "no active/authenticated account for this configuration"}, nil
}

func init() { provider.Register(New()) }
