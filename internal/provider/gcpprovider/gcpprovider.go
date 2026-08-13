// Package gcpprovider adapts gcloud named configurations
// (internal/gcpcreds) to the generic provider.Provider interface and
// registers it as "gcp".
//
// Import for side effects to make the provider available:
//
//	import _ "ez-cloud-manager/internal/provider/gcpprovider"
package gcpprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ez-cloud-manager/internal/gcpcreds"
	"ez-cloud-manager/internal/provider"
)

const id = "gcp"

type gcpProvider struct{}

var _ provider.ConditionalSaver = gcpProvider{}

// SaveBatchIfUnchanged is consumed by the GCP sign-in synchronizer. It keeps
// the concrete batch DTO in gcpcreds so the provider-wide CRUD contract stays
// small while still preserving provider.ErrConnectionConflict at the public
// boundary.
func (gcpProvider) SaveBatchIfUnchanged(path string, changes []gcpcreds.ConditionalSave) error {
	err := gcpcreds.SaveBatchIfUnchanged(path, changes)
	if errors.Is(err, gcpcreds.ErrConflict) {
		return provider.ErrConnectionConflict
	}
	return err
}

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

func (gcpProvider) SaveIfUnchanged(path, name string, fields, expectedFields map[string]string, expectAbsent bool) error {
	err := gcpcreds.SaveIfUnchanged(path, name, fields, expectedFields, expectAbsent)
	if errors.Is(err, gcpcreds.ErrConflict) {
		return provider.ErrConnectionConflict
	}
	return err
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

func (gcpProvider) ActivateLabel() string { return "Activate globally in gcloud" }

// Check captures the selected configuration into a temporary non-active
// configuration containing only core.account/core.project. gcloud still owns
// and reads its credential database, but mutable impersonation, proxy,
// endpoint and TLS properties from the source configuration cannot affect the
// verification request.
func (gcpProvider) Check(ctx context.Context, path, name string) (provider.CheckResult, error) {
	stored, err := gcpcreds.Get(path, name)
	if err != nil {
		return provider.CheckResult{}, err
	}
	snapshot, snapshotErr := prepareGCPCheckSnapshot(path, stored.Fields)
	if snapshotErr != nil {
		return provider.CheckResult{OK: false, Error: "gcloud configuration could not be isolated for a safe verification"}, nil
	}
	defer func() { _ = snapshot.cleanup() }()
	if snapshot.project != "" {
		return checkGCPProject(ctx, path, &snapshot)
	}

	result, runErr := provider.RunVendorCommand(
		ctx,
		"gcloud",
		[]string{
			"auth", "list", "--filter=status:ACTIVE", "--format=json(account,status)",
			"--configuration", snapshot.name,
		},
		map[string]string{"CLOUDSDK_CONFIG": path},
		1<<20,
	)
	if errors.Is(runErr, context.DeadlineExceeded) {
		return provider.CheckResult{OK: false, Error: "timed out waiting for gcloud auth list"}, nil
	}
	if runErr != nil {
		return provider.CheckResult{OK: false, Error: "gcloud could not verify credentials for this configuration"}, nil
	}
	if cleanupErr := snapshot.cleanup(); cleanupErr != nil {
		return provider.CheckResult{}, fmt.Errorf("remove isolated gcloud verification files: %w", cleanupErr)
	}

	var accounts []struct {
		Account string
		Status  string
	}
	if err := json.Unmarshal(result.Stdout, &accounts); err != nil {
		return provider.CheckResult{OK: false, Error: "gcloud returned malformed account JSON"}, nil
	}
	for _, a := range accounts {
		if a.Status == "ACTIVE" && strings.TrimSpace(a.Account) == snapshot.account {
			return provider.CheckResult{OK: true, Identity: map[string]string{
				"account":      a.Account,
				"verification": "credentials-present",
			}}, nil
		}
	}
	return provider.CheckResult{OK: false, Error: "no active/authenticated account for this configuration"}, nil
}

func checkGCPProject(ctx context.Context, root string, snapshot *gcpCheckSnapshot) (provider.CheckResult, error) {
	result, runErr := provider.RunVendorCommand(
		ctx,
		"gcloud",
		[]string{
			"projects", "describe", snapshot.project,
			"--configuration", snapshot.name,
			"--format=json(projectId,projectNumber)",
		},
		map[string]string{"CLOUDSDK_CONFIG": root},
		1<<20,
	)
	if errors.Is(runErr, context.DeadlineExceeded) {
		return provider.CheckResult{OK: false, Error: "timed out verifying the Google Cloud project"}, nil
	}
	if runErr != nil {
		return provider.CheckResult{OK: false, Error: "gcloud could not verify this Google Cloud project"}, nil
	}
	if cleanupErr := snapshot.cleanup(); cleanupErr != nil {
		return provider.CheckResult{}, fmt.Errorf("remove isolated gcloud verification files: %w", cleanupErr)
	}
	var identity struct {
		ProjectID     string `json:"projectId"`
		ProjectNumber string `json:"projectNumber"`
	}
	if err := json.Unmarshal(result.Stdout, &identity); err != nil {
		return provider.CheckResult{OK: false, Error: "gcloud returned malformed project JSON"}, nil
	}
	if identity.ProjectID != snapshot.project || identity.ProjectNumber == "" {
		return provider.CheckResult{OK: false, Error: fmt.Sprintf("gcloud returned an unexpected project identity for %q", snapshot.project)}, nil
	}
	return provider.CheckResult{OK: true, Identity: map[string]string{
		"project":       identity.ProjectID,
		"projectNumber": identity.ProjectNumber,
		"verification":  "live",
	}}, nil
}

func init() { provider.Register(New()) }
