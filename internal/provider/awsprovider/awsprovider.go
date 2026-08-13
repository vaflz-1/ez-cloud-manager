// Package awsprovider adapts the AWS credentials backend (internal/awscreds)
// to the generic provider.Provider interface and registers it as "aws".
//
// Import for side effects to make the provider available:
//
//	import _ "ez-cloud-manager/internal/provider/awsprovider"
package awsprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ez-cloud-manager/internal/awscreds"
	"ez-cloud-manager/internal/provider"
)

const id = "aws"

// awsProvider delegates every operation to the existing awscreds package,
// converting between its concrete types and the provider-agnostic DTOs.
type awsProvider struct{}

var _ provider.ConditionalSaver = awsProvider{}

// New returns the AWS credentials provider, backed by ~/.aws/credentials.
func New() provider.Provider { return awsProvider{} }

func (awsProvider) ID() string          { return id }
func (awsProvider) DisplayName() string { return "AWS" }

func (awsProvider) DefaultPath() (string, error) { return awscreds.DefaultPath() }

func (awsProvider) List(path string) ([]provider.ProfileSummary, error) {
	summaries, err := awscreds.List(path)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProfileSummary, 0, len(summaries))
	seen := make(map[string]bool, len(summaries))
	for _, s := range summaries {
		seen[s.Name] = true
		out = append(out, provider.ProfileSummary{Name: s.Name, Keys: s.Keys})
	}
	configPath, configErr := configPathFor(path)
	if configErr == nil {
		configProfiles, configErr := awscreds.ListConfigProfiles(configPath)
		if configErr != nil {
			return nil, configErr
		}
		for _, s := range configProfiles {
			profile, err := awscreds.GetConfigProfile(configPath, s.Name)
			if err != nil {
				return nil, err
			}
			if !isAWSManagedSSO(profile.Fields) || hasUnsafeSSOResolution(profile.Fields) {
				continue
			}
			if seen[s.Name] {
				// A credentials section with the same name wins in AWS's
				// effective resolution. Do not present this as a safe SSO
				// connection; auth discovery will surface the collision for
				// explicit review instead.
				continue
			}
			out = append(out, provider.ProfileSummary{
				Name: s.Name, Keys: s.Keys, Source: "sso", ReadOnly: true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (awsProvider) Get(path, name string) (provider.Profile, error) {
	p, err := awscreds.Get(path, name)
	if err != nil {
		return provider.Profile{}, err
	}
	if len(p.Fields) > 0 {
		return provider.Profile{Name: p.Name, Fields: p.Fields}, nil
	}
	configPath, err := configPathFor(path)
	if err != nil {
		return provider.Profile{}, err
	}
	configProfile, err := awscreds.GetConfigProfile(configPath, name)
	if err != nil {
		return provider.Profile{}, err
	}
	if isAWSManagedSSO(configProfile.Fields) && !hasUnsafeSSOResolution(configProfile.Fields) {
		return provider.Profile{Name: configProfile.Name, Fields: safeSSOFields(configProfile.Fields)}, nil
	}
	if isAWSManagedSSO(configProfile.Fields) {
		return provider.Profile{}, fmt.Errorf(
			"AWS SSO profile %q contains credential or endpoint overrides and is blocked for safety",
			name,
		)
	}
	return provider.Profile{Name: p.Name, Fields: p.Fields}, nil
}

func isAWSManagedSSO(fields map[string]string) bool {
	return strings.TrimSpace(fields[awscreds.KeySSOSession]) != "" ||
		(strings.TrimSpace(fields[awscreds.KeySSOStartURL]) != "" &&
			strings.TrimSpace(fields[awscreds.KeySSORegion]) != "")
}

// hasUnsafeSSOResolution rejects settings that can replace the selected SSO
// identity, execute a local helper, redirect vendor traffic, or alter TLS
// trust. Auto-discovered profiles have not been authored or reviewed inside
// Kervik, so they must be stricter than manually-managed credential records.
func hasUnsafeSSOResolution(fields map[string]string) bool {
	unsafe := []string{
		awscreds.KeyAccessKeyID,
		awscreds.KeySecretAccessKey,
		awscreds.KeySessionToken,
		awscreds.KeyCredentialProc,
		awscreds.KeyCredentialSrc,
		awscreds.KeyRoleArn,
		awscreds.KeySourceProfile,
		awscreds.KeyWebIdentityFile,
		awscreds.KeyEndpointURL,
		awscreds.KeyCABundle,
		"login_session",
		"services",
	}
	for _, key := range unsafe {
		if strings.TrimSpace(fields[key]) != "" {
			return true
		}
	}
	return false
}

func safeSSOFields(fields map[string]string) map[string]string {
	allowed := []string{
		awscreds.KeySSOSession,
		awscreds.KeySSORegion,
		awscreds.KeySSOAccountID,
		awscreds.KeySSORoleName,
		awscreds.KeyRegion,
		awscreds.KeyOutput,
	}
	out := make(map[string]string, len(allowed))
	for _, key := range allowed {
		if value := strings.TrimSpace(fields[key]); value != "" {
			out[key] = value
		}
	}
	return out
}

func (awsProvider) Save(path, name string, fields map[string]string) error {
	managed, err := isExternalSSOProfile(path, name)
	if err != nil {
		return err
	}
	if managed || isAWSManagedSSO(fields) {
		return fmt.Errorf("AWS SSO profiles are managed by the AWS CLI; use Sign In / Sync instead of saving them as credentials")
	}
	return awscreds.Save(path, name, fields)
}

func (awsProvider) SaveIfUnchanged(path, name string, fields, expectedFields map[string]string, expectAbsent bool) error {
	managed, err := isExternalSSOProfile(path, name)
	if err != nil {
		return err
	}
	if managed || isAWSManagedSSO(fields) || isAWSManagedSSO(expectedFields) {
		return fmt.Errorf("AWS SSO profiles are managed by the AWS CLI; use Sign In / Sync instead of saving them as credentials")
	}
	err = awscreds.SaveIfUnchanged(path, name, fields, expectedFields, expectAbsent)
	if errors.Is(err, awscreds.ErrConflict) {
		return provider.ErrConnectionConflict
	}
	return err
}

func isExternalSSOProfile(credentialsPath, name string) (bool, error) {
	configPath, err := configPathFor(credentialsPath)
	if err != nil {
		return false, err
	}
	profile, err := awscreds.GetConfigProfile(configPath, name)
	if err != nil {
		return false, err
	}
	return isAWSManagedSSO(profile.Fields), nil
}

func (awsProvider) Delete(path, name string) error {
	configPath, err := configPathFor(path)
	if err == nil {
		configProfile, getErr := awscreds.GetConfigProfile(configPath, name)
		if getErr == nil && isAWSManagedSSO(configProfile.Fields) {
			return fmt.Errorf("AWS SSO profile %q is managed by the AWS CLI and cannot be deleted here", name)
		}
	}
	return awscreds.Delete(path, name)
}

func configPathFor(credentialsPath string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("AWS_CONFIG_FILE")); override != "" {
		return override, nil
	}
	defaultCredentials, err := awscreds.DefaultPath()
	if err != nil {
		return "", err
	}
	if filepath.Clean(credentialsPath) == filepath.Clean(defaultCredentials) {
		return awscreds.DefaultConfigPath()
	}
	// Directly-injected/test credential paths are isolated from the real home.
	return filepath.Join(filepath.Dir(credentialsPath), "config"), nil
}

func (awsProvider) Parse(text string) provider.Parsed {
	p := awscreds.Parse(text)
	return provider.Parsed{ProfileName: p.ProfileName, Fields: p.Fields}
}

// Schema mirrors the field knowledge the UI previously hardcoded: display
// labels, canonical env-var names for export, secret/common grouping and
// placeholder examples. Order is UI order.
func (awsProvider) Schema() provider.Schema {
	return provider.Schema{
		Provider:    id,
		DisplayName: "AWS",
		Fields: []provider.FieldSpec{
			{Key: awscreds.KeyAccessKeyID, Display: "AWS_ACCESS_KEY_ID", Env: "AWS_ACCESS_KEY_ID", Common: true, Placeholder: "AKIA…"},
			{Key: awscreds.KeySecretAccessKey, Display: "AWS_SECRET_ACCESS_KEY", Env: "AWS_SECRET_ACCESS_KEY", Secret: true, Common: true, Placeholder: "Not set — click the eye to add"},
			{Key: awscreds.KeySessionToken, Display: "AWS_SESSION_TOKEN", Env: "AWS_SESSION_TOKEN", Secret: true, Common: true, Placeholder: "Not set — click the eye to add"},
			{Key: awscreds.KeyRegion, Display: "AWS_DEFAULT_REGION", Env: "AWS_DEFAULT_REGION", Common: true, Placeholder: "us-east-1"},
			{Key: awscreds.KeyOutput, Display: "AWS_DEFAULT_OUTPUT", Env: "AWS_DEFAULT_OUTPUT", Common: true, Placeholder: "json"},
			{Key: awscreds.KeyRoleArn, Display: "AWS_ROLE_ARN / role_arn", Env: "AWS_ROLE_ARN", Placeholder: "arn:aws:iam::123456789012:role/Name"},
			{Key: awscreds.KeySourceProfile, Display: "source_profile", Placeholder: "default"},
			{Key: awscreds.KeyMfaSerial, Display: "mfa_serial", Placeholder: "arn:aws:iam::123456789012:mfa/user"},
			{Key: awscreds.KeyDurationSeconds, Display: "duration_seconds", Placeholder: "3600"},
			{Key: awscreds.KeyCredentialProc, Display: "credential_process", Placeholder: "/path/to/helper"},
			{Key: awscreds.KeyCredentialSrc, Display: "credential_source", Placeholder: "Ec2InstanceMetadata"},
			{Key: awscreds.KeyRoleSessionName, Display: "AWS_ROLE_SESSION_NAME", Env: "AWS_ROLE_SESSION_NAME", Placeholder: "my-session"},
			{Key: awscreds.KeyExternalID, Display: "external_id", Placeholder: "unique-id"},
			{Key: awscreds.KeyWebIdentityFile, Display: "AWS_WEB_IDENTITY_TOKEN_FILE", Env: "AWS_WEB_IDENTITY_TOKEN_FILE", Placeholder: "/path/to/token"},
			{Key: awscreds.KeyEndpointURL, Display: "AWS_ENDPOINT_URL", Env: "AWS_ENDPOINT_URL", Placeholder: "https://…"},
			{Key: awscreds.KeyCLIPager, Display: "AWS_CLI_PAGER", Env: "AWS_PAGER", Placeholder: "(empty to disable)"},
			{Key: awscreds.KeyRetryMode, Display: "AWS_RETRY_MODE", Env: "AWS_RETRY_MODE", Placeholder: "standard"},
			{Key: awscreds.KeyMaxAttempts, Display: "AWS_MAX_ATTEMPTS", Env: "AWS_MAX_ATTEMPTS", Placeholder: "3"},
			{Key: awscreds.KeySTSEndpoints, Display: "AWS_STS_REGIONAL_ENDPOINTS", Env: "AWS_STS_REGIONAL_ENDPOINTS", Placeholder: "regional"},
			// IAM Identity Center fields live in ~/.aws/config, not the
			// credentials store edited by this schema. They are rendered as
			// read-only extras for discovered SSO profiles and created only
			// through the explicit Sign In / Sync flow.
		},
	}
}

// Activate copies the named profile's fields over the [default] profile —
// the file-level equivalent of "make this my default AWS identity". The
// source profile is left untouched.
func (p awsProvider) Activate(path, name string) error {
	prof, err := awscreds.Get(path, name)
	if err != nil {
		return err
	}
	if len(prof.Fields) == 0 {
		return fmt.Errorf("profile %q has no fields to copy to [default]", name)
	}
	// Clear stale default-only keys: Save merges, so explicitly blank out
	// keys currently in [default] that the source profile does not carry.
	current, err := awscreds.Get(path, "default")
	if err != nil {
		return err
	}
	fields := make(map[string]string, len(prof.Fields))
	for key := range current.Fields {
		fields[key] = ""
	}
	for key, value := range prof.Fields {
		fields[key] = value
	}
	return awscreds.Save(path, "default", fields)
}

func (awsProvider) ActivateLabel() string { return "Copy to [default] profile" }

// Check runs `aws sts get-caller-identity` for the named profile — the
// standard vendor-CLI liveness call, requiring no permissions beyond
// identifying the caller. path is the credentials file (see
// awscreds.DefaultPath); AWS_SHARED_CREDENTIALS_FILE scopes the aws CLI to
// it without touching the user's real ~/.aws/credentials. A missing aws
// binary or a failed call is reported via CheckResult, never a Go error —
// this call's own error return is reserved for something that prevented it
// from even attempting the check.
func (awsProvider) Check(ctx context.Context, path, name string) (provider.CheckResult, error) {
	configPath, configErr := configPathFor(path)
	if configErr != nil {
		return provider.CheckResult{}, configErr
	}
	snapshot, snapshotErr := prepareAWSCheckSnapshot(path, configPath, name)
	if snapshotErr != nil {
		return provider.CheckResult{
			OK:    false,
			Error: "AWS connection could not be isolated for a safe verification",
		}, nil
	}
	defer func() { _ = snapshot.cleanup() }()

	result, runErr := provider.RunVendorCommand(
		ctx,
		"aws",
		[]string{"sts", "get-caller-identity", "--profile", name, "--output", "json", "--no-cli-pager"},
		map[string]string{
			"AWS_CLI_AUTO_PROMPT":         "off",
			"AWS_CONFIG_FILE":             snapshot.configPath,
			"AWS_EC2_METADATA_DISABLED":   "true",
			"AWS_PAGER":                   "",
			"AWS_SHARED_CREDENTIALS_FILE": snapshot.credentialsPath,
		},
		1<<20,
	)
	if errors.Is(runErr, context.DeadlineExceeded) {
		return provider.CheckResult{OK: false, Error: "timed out waiting for aws sts get-caller-identity"}, nil
	}
	if runErr != nil {
		// Vendor diagnostics may include account identifiers, browser URLs,
		// credential-process output, or temporary auth material. Keep them out
		// of the app-facing JSON just as the interactive auth runner does.
		return provider.CheckResult{OK: false, Error: "AWS CLI could not verify this connection"}, nil
	}
	if cleanupErr := snapshot.cleanup(); cleanupErr != nil {
		return provider.CheckResult{}, fmt.Errorf("remove isolated AWS verification files: %w", cleanupErr)
	}

	var identity struct {
		Account string
		Arn     string
		UserId  string
	}
	if err := json.Unmarshal(result.Stdout, &identity); err != nil {
		return provider.CheckResult{OK: false, Error: "AWS CLI returned malformed identity JSON"}, nil
	}
	if strings.TrimSpace(identity.Account) == "" || strings.TrimSpace(identity.Arn) == "" {
		return provider.CheckResult{OK: false, Error: "AWS CLI returned an incomplete caller identity"}, nil
	}
	if snapshot.expectedAccount != "" && identity.Account != snapshot.expectedAccount {
		return provider.CheckResult{OK: false, Error: "AWS CLI returned a different account than the SSO profile declares"}, nil
	}
	return provider.CheckResult{OK: true, Identity: map[string]string{
		"account": identity.Account,
		"arn":     identity.Arn,
		"userId":  identity.UserId,
	}}, nil
}

func init() { provider.Register(New()) }
