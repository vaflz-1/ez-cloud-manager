package awsprovider

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"ez-cloud-manager/internal/awscreds"
	"ez-cloud-manager/internal/inifile"
)

const maxAWSCheckFileBytes = 4 << 20

var (
	checkAWSAccountID = regexp.MustCompile(`^[0-9]{12}$`)
	checkAWSRegion    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
	checkAWSRole      = regexp.MustCompile(`^[A-Za-z0-9_+=,.@-]{1,64}$`)
)

type awsCheckSnapshot struct {
	root            string
	configPath      string
	credentialsPath string
	expectedAccount string
}

// ExecutionSnapshot is a captured, private AWS profile used by feature
// operations after the Platform has authorized a Connection. It deliberately
// exposes only sanitized file paths: callers cannot accidentally pass the raw
// mutable config/credentials files to the AWS CLI after validation.
type ExecutionSnapshot struct {
	inner awsCheckSnapshot
}

// PrepareExecutionSnapshot captures one direct-credential or managed SSO
// profile and rejects delegated credential helpers, role/source chaining,
// endpoint routing and custom trust settings. The returned files are 0700/0600
// and immutable for the lifetime of the request.
func PrepareExecutionSnapshot(credentialsPath, configPath, name string) (*ExecutionSnapshot, error) {
	snapshot, err := prepareAWSCheckSnapshot(credentialsPath, configPath, name)
	if err != nil {
		return nil, err
	}
	return &ExecutionSnapshot{inner: snapshot}, nil
}

// VendorOverrides returns the only AWS configuration paths a feature runner
// may pass to the vendor CLI.
func (s *ExecutionSnapshot) VendorOverrides() map[string]string {
	if s == nil {
		return nil
	}
	return map[string]string{
		"AWS_CONFIG_FILE":             s.inner.configPath,
		"AWS_SHARED_CREDENTIALS_FILE": s.inner.credentialsPath,
	}
}

// Close removes the private captured files.
func (s *ExecutionSnapshot) Close() error {
	if s == nil {
		return nil
	}
	return s.inner.cleanup()
}

func (s *awsCheckSnapshot) cleanup() error {
	if s.root == "" {
		return nil
	}
	root := s.root
	s.root = ""
	return os.RemoveAll(root)
}

// prepareAWSCheckSnapshot captures the selected connection once and renders
// only the fields needed by STS into a private temporary configuration. The
// vendor CLI never re-opens the mutable source files, so a concurrent writer
// cannot inject credential_process, endpoint or trust overrides between the
// app's review and execution.
func prepareAWSCheckSnapshot(credentialsPath, configPath, name string) (awsCheckSnapshot, error) {
	name = strings.TrimSpace(name)
	if !safeAWSSectionName(name) || strings.HasPrefix(name, "-") {
		return awsCheckSnapshot{}, fmt.Errorf("unsafe AWS profile name")
	}
	credentialsModel, err := inifile.ReadLimited(credentialsPath, maxAWSCheckFileBytes)
	if err != nil {
		return awsCheckSnapshot{}, fmt.Errorf("read AWS credentials snapshot: %w", err)
	}
	configModel, err := inifile.ReadLimited(configPath, maxAWSCheckFileBytes)
	if err != nil {
		return awsCheckSnapshot{}, fmt.Errorf("read AWS config snapshot: %w", err)
	}

	credentialFields, credentialFound, err := uniqueAWSSection(credentialsModel, name)
	if err != nil {
		return awsCheckSnapshot{}, err
	}
	configSection := "profile " + name
	if name == "default" {
		configSection = "default"
	}
	configFields, configFound, err := uniqueAWSSection(configModel, configSection)
	if err != nil {
		return awsCheckSnapshot{}, err
	}

	outputConfig := inifile.Model{}
	outputCredentials := inifile.Model{}
	expectedAccount := ""
	if credentialFound && hasDirectAWSCredentials(credentialFields) {
		if hasUnsafeDirectCredentialResolution(credentialFields) {
			return awsCheckSnapshot{}, errors.New("AWS profile uses delegated credential or routing settings and cannot be tested safely")
		}
		if strings.TrimSpace(credentialFields[awscreds.KeyAccessKeyID]) == "" ||
			strings.TrimSpace(credentialFields[awscreds.KeySecretAccessKey]) == "" {
			return awsCheckSnapshot{}, errors.New("AWS profile has incomplete direct credentials")
		}
		section := inifile.Section{Name: name}
		section.ApplyFields(copyAWSFields(credentialFields, []string{
			awscreds.KeyAccessKeyID, awscreds.KeySecretAccessKey, awscreds.KeySessionToken,
		}))
		outputCredentials.Sections = append(outputCredentials.Sections, section)
		if configFound {
			profile := inifile.Section{Name: configSection}
			profile.ApplyFields(copyAWSFields(configFields, []string{awscreds.KeyRegion, awscreds.KeyOutput}))
			outputConfig.Sections = append(outputConfig.Sections, profile)
		}
	} else {
		if !configFound || !isAWSManagedSSO(configFields) || hasUnsafeSSOResolution(configFields) {
			return awsCheckSnapshot{}, errors.New("AWS profile is not a safe direct-credential or SSO connection")
		}
		expectedAccount = strings.TrimSpace(configFields[awscreds.KeySSOAccountID])
		role := strings.TrimSpace(configFields[awscreds.KeySSORoleName])
		if !checkAWSAccountID.MatchString(expectedAccount) || !checkAWSRole.MatchString(role) {
			return awsCheckSnapshot{}, errors.New("AWS SSO profile has invalid account or role metadata")
		}
		profileFields := copyAWSFields(configFields, []string{
			awscreds.KeySSOSession, awscreds.KeySSOStartURL, awscreds.KeySSORegion,
			awscreds.KeySSOAccountID, awscreds.KeySSORoleName, awscreds.KeyRegion, awscreds.KeyOutput,
		})
		profile := inifile.Section{Name: configSection}
		profile.ApplyFields(profileFields)
		outputConfig.Sections = append(outputConfig.Sections, profile)

		sessionName := strings.TrimSpace(configFields[awscreds.KeySSOSession])
		startURL := strings.TrimSpace(configFields[awscreds.KeySSOStartURL])
		ssoRegion := strings.TrimSpace(configFields[awscreds.KeySSORegion])
		if sessionName != "" {
			if !safeAWSSectionName(sessionName) || strings.HasPrefix(sessionName, "-") {
				return awsCheckSnapshot{}, errors.New("AWS SSO session name is unsafe")
			}
			sessionFields, found, err := uniqueAWSSection(configModel, "sso-session "+sessionName)
			if err != nil {
				return awsCheckSnapshot{}, err
			}
			if !found {
				return awsCheckSnapshot{}, errors.New("AWS SSO session is missing")
			}
			startURL = strings.TrimSpace(sessionFields[awscreds.KeySSOStartURL])
			ssoRegion = strings.TrimSpace(sessionFields[awscreds.KeySSORegion])
			session := inifile.Section{Name: "sso-session " + sessionName}
			session.ApplyFields(map[string]string{
				awscreds.KeySSOStartURL:   startURL,
				awscreds.KeySSORegion:     ssoRegion,
				"sso_registration_scopes": "sso:account:access",
			})
			outputConfig.Sections = append(outputConfig.Sections, session)
		}
		if !safeAWSStartURL(startURL) || !checkAWSRegion.MatchString(ssoRegion) {
			return awsCheckSnapshot{}, errors.New("AWS SSO portal metadata is incomplete or unsafe")
		}
	}

	root, err := os.MkdirTemp("", "kervik-aws-check-")
	if err != nil {
		return awsCheckSnapshot{}, fmt.Errorf("create isolated AWS check: %w", err)
	}
	snapshot := awsCheckSnapshot{
		root: root, configPath: filepath.Join(root, "config"),
		credentialsPath: filepath.Join(root, "credentials"), expectedAccount: expectedAccount,
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = snapshot.cleanup()
		return awsCheckSnapshot{}, fmt.Errorf("secure isolated AWS check: %w", err)
	}
	if err := os.WriteFile(snapshot.configPath, inifile.Render(outputConfig), 0o600); err != nil {
		_ = snapshot.cleanup()
		return awsCheckSnapshot{}, fmt.Errorf("write isolated AWS config: %w", err)
	}
	if err := os.WriteFile(snapshot.credentialsPath, inifile.Render(outputCredentials), 0o600); err != nil {
		_ = snapshot.cleanup()
		return awsCheckSnapshot{}, fmt.Errorf("write isolated AWS credentials: %w", err)
	}
	return snapshot, nil
}

func uniqueAWSSection(model inifile.Model, name string) (map[string]string, bool, error) {
	var fields map[string]string
	for _, section := range model.Sections {
		if section.Name != name {
			continue
		}
		if fields != nil {
			return nil, false, fmt.Errorf("AWS file contains duplicate section %q", name)
		}
		var err error
		fields, err = section.StrictFields()
		if err != nil {
			return nil, false, err
		}
	}
	return fields, fields != nil, nil
}

func safeAWSSectionName(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "[]\r\n\x00") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func hasDirectAWSCredentials(fields map[string]string) bool {
	return strings.TrimSpace(fields[awscreds.KeyAccessKeyID]) != "" ||
		strings.TrimSpace(fields[awscreds.KeySecretAccessKey]) != "" ||
		strings.TrimSpace(fields[awscreds.KeySessionToken]) != ""
}

func hasUnsafeDirectCredentialResolution(fields map[string]string) bool {
	for _, key := range []string{
		awscreds.KeyCredentialProc, awscreds.KeyCredentialSrc, awscreds.KeyRoleArn,
		awscreds.KeySourceProfile, awscreds.KeyWebIdentityFile, awscreds.KeyEndpointURL,
		awscreds.KeyCABundle, awscreds.KeySSOSession, awscreds.KeySSOStartURL,
		"login_session", "services",
	} {
		if strings.TrimSpace(fields[key]) != "" {
			return true
		}
	}
	return false
}

func copyAWSFields(fields map[string]string, allowed []string) map[string]string {
	out := make(map[string]string, len(allowed))
	for _, key := range allowed {
		if value := strings.TrimSpace(fields[key]); value != "" {
			out[key] = value
		}
	}
	return out
}

func safeAWSStartURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
