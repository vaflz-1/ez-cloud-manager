// Package awsprovider adapts the AWS credentials backend (internal/awscreds)
// to the generic provider.Provider interface and registers it as "aws".
//
// Import for side effects to make the provider available:
//
//	import _ "ez-cloud-manager/internal/provider/awsprovider"
package awsprovider

import (
	"fmt"

	"ez-cloud-manager/internal/awscreds"
	"ez-cloud-manager/internal/provider"
)

const id = "aws"

// awsProvider delegates every operation to the existing awscreds package,
// converting between its concrete types and the provider-agnostic DTOs.
type awsProvider struct{}

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
	out := make([]provider.ProfileSummary, len(summaries))
	for i, s := range summaries {
		out[i] = provider.ProfileSummary{Name: s.Name, Keys: s.Keys}
	}
	return out, nil
}

func (awsProvider) Get(path, name string) (provider.Profile, error) {
	p, err := awscreds.Get(path, name)
	if err != nil {
		return provider.Profile{}, err
	}
	return provider.Profile{Name: p.Name, Fields: p.Fields}, nil
}

func (awsProvider) Save(path, name string, fields map[string]string) error {
	return awscreds.Save(path, name, fields)
}

func (awsProvider) Delete(path, name string) error {
	return awscreds.Delete(path, name)
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
			{Key: awscreds.KeySSOSession, Display: "sso_session", Placeholder: "my-sso"},
			{Key: awscreds.KeySSOStartURL, Display: "sso_start_url", Placeholder: "https://my.awsapps.com/start"},
			{Key: awscreds.KeySSORegion, Display: "sso_region", Placeholder: "us-east-1"},
			{Key: awscreds.KeySSOAccountID, Display: "sso_account_id", Placeholder: "123456789012"},
			{Key: awscreds.KeySSORoleName, Display: "sso_role_name", Placeholder: "PowerUserAccess"},
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

func init() { provider.Register(New()) }
