package connectionsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ez-cloud-manager/internal/inifile"
)

const maxAWSConfigBytes = 4 << 20

var awsAccountIDPattern = regexp.MustCompile(`^[0-9]{12}$`)
var awsRegionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
var awsRoleNamePattern = regexp.MustCompile(`^[A-Za-z0-9_+=,.@-]{1,64}$`)

type awsSource struct {
	Candidate   Candidate
	SessionName string
	StartURL    string
	SSORegion   string
	PortalHash  string
}

type awsRevisionEntry struct {
	Candidate   Candidate `json:"candidate"`
	SessionName string    `json:"sessionName,omitempty"`
	PortalHash  string    `json:"portalHash,omitempty"`
	SSORegion   string    `json:"ssoRegion,omitempty"`
}

type awsSTSIdentity struct {
	Account string `json:"Account"`
}

func (m *Manager) discoverAWS() (DiscoverySnapshot, map[string]awsSource, error) {
	warnings := []string{}
	model, err := inifile.ReadLimited(m.awsConfigPath, maxAWSConfigBytes)
	if err != nil {
		return DiscoverySnapshot{}, nil, fmt.Errorf("read AWS shared config: %w", err)
	}
	profiles := map[string]map[string]string{}
	sessions := map[string]map[string]string{}
	credentialCollisions, err := readAWSCredentialProfiles(m.awsCredentialsPath)
	if err != nil {
		return DiscoverySnapshot{}, nil, err
	}
	unsafeSections := 0
	for _, section := range model.Sections {
		name := strings.TrimSpace(section.Name)
		switch {
		case name == "default":
			profiles["default"] = mergeFields(profiles["default"], section.Fields())
		case strings.HasPrefix(name, "profile "):
			profileName := strings.TrimSpace(strings.TrimPrefix(name, "profile "))
			if !safeIdentifier(profileName, 256) {
				unsafeSections++
				continue
			}
			profiles[profileName] = mergeFields(profiles[profileName], section.Fields())
		case strings.HasPrefix(name, "sso-session "):
			sessionName := strings.TrimSpace(strings.TrimPrefix(name, "sso-session "))
			if !safeIdentifier(sessionName, 256) {
				unsafeSections++
				continue
			}
			sessions[sessionName] = mergeFields(sessions[sessionName], section.Fields())
		}
	}
	if unsafeSections > 0 {
		warnings = append(warnings, fmt.Sprintf("Ignored %d AWS config section(s) with unsafe names.", unsafeSections))
	}

	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	sources := make(map[string]awsSource, len(names))
	entries := make([]awsRevisionEntry, 0, len(names))
	for _, name := range names {
		fields := profiles[name]
		sessionName := strings.TrimSpace(fields["sso_session"])
		hasSSO := sessionName != "" || fields["sso_start_url"] != "" || fields["sso_region"] != "" || fields["sso_account_id"] != "" || fields["sso_role_name"] != ""
		if !hasSSO {
			continue
		}

		authMode := "legacy"
		startURL := strings.TrimSpace(fields["sso_start_url"])
		ssoRegion := strings.TrimSpace(fields["sso_region"])
		if sessionName != "" {
			authMode = "sso-session"
			sessionFields := sessions[sessionName]
			startURL = strings.TrimSpace(sessionFields["sso_start_url"])
			ssoRegion = strings.TrimSpace(sessionFields["sso_region"])
		}

		accountID := strings.TrimSpace(fields["sso_account_id"])
		roleName := strings.TrimSpace(fields["sso_role_name"])
		region := strings.TrimSpace(fields["region"])
		candidate := Candidate{
			ID:            stableID("aws", name),
			Name:          name,
			DisplayName:   name,
			SourceProfile: name,
			AuthMode:      authMode,
			AccountID:     accountID,
			RoleName:      roleName,
			Region:        region,
			Status:        StatusNew,
		}
		switch {
		case strings.HasPrefix(name, "-"):
			candidate.Reason = "AWS profile names beginning with a dash cannot be used safely by the CLI."
		case credentialCollisions[name]:
			candidate.Reason = "Rename or remove the matching credentials-file connection first; AWS would otherwise use it instead of SSO."
		case hasAWSCredentialResolutionFields(fields):
			candidate.Reason = "The AWS config profile also defines a static or delegated credential source."
		case sessionName != "" && (!safeIdentifier(sessionName, 256) || strings.HasPrefix(sessionName, "-")):
			candidate.Reason = "SSO session name is invalid."
		case !validAWSStartURL(startURL):
			candidate.Reason = "SSO portal configuration is incomplete."
		case !awsRegionPattern.MatchString(ssoRegion):
			candidate.Reason = "SSO region is missing or invalid."
		case !awsAccountIDPattern.MatchString(accountID):
			candidate.Reason = "A 12-digit AWS account ID is required."
		case !awsRoleNamePattern.MatchString(roleName):
			candidate.Reason = "AWS role name is missing or invalid."
		case region != "" && !awsRegionPattern.MatchString(region):
			candidate.Reason = "Default AWS region is invalid."
		default:
			candidate.CanApply = true
		}

		source := awsSource{
			Candidate:   candidate,
			SessionName: sessionName,
			StartURL:    startURL,
			SSORegion:   ssoRegion,
			PortalHash:  stableID("aws-portal", startURL),
		}
		sources[candidate.ID] = source
		entries = append(entries, awsRevisionEntry{
			Candidate: candidate, SessionName: sessionName,
			PortalHash: source.PortalHash, SSORegion: ssoRegion,
		})
		if len(entries) > maxAWSSSOProfiles {
			return DiscoverySnapshot{}, nil, fmt.Errorf("AWS shared config contains more than %d SSO profiles", maxAWSSSOProfiles)
		}
	}

	rev, err := revision(entries)
	if err != nil {
		return DiscoverySnapshot{}, nil, err
	}
	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		candidates = append(candidates, entry.Candidate)
	}
	return DiscoverySnapshot{
		ProtocolVersion: ProtocolVersion,
		Provider:        "aws",
		Revision:        rev,
		Candidates:      candidates,
		Warnings:        warnings,
	}, sources, nil
}

func validAWSStartURL(raw string) bool {
	if !safeIdentifier(raw, 2048) {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

var awsCredentialResolutionKeys = map[string]bool{
	"aws_access_key_id":       true,
	"aws_secret_access_key":   true,
	"aws_session_token":       true,
	"ca_bundle":               true,
	"credential_process":      true,
	"credential_source":       true,
	"endpoint_url":            true,
	"login_session":           true,
	"role_arn":                true,
	"services":                true,
	"source_profile":          true,
	"web_identity_token_file": true,
}

func hasAWSCredentialResolutionFields(fields map[string]string) bool {
	for key, value := range fields {
		if awsCredentialResolutionKeys[strings.ToLower(strings.TrimSpace(key))] && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func readAWSCredentialProfiles(path string) (map[string]bool, error) {
	collisions := map[string]bool{}
	model, err := inifile.ReadLimited(path, maxAWSConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("read AWS shared credentials file: %w", err)
	}
	for _, section := range model.Sections {
		name := strings.TrimSpace(section.Name)
		if !safeIdentifier(name, 256) {
			continue
		}
		if hasAWSCredentialResolutionFields(section.Fields()) {
			collisions[name] = true
		}
	}
	return collisions, nil
}

func mergeFields(existing, next map[string]string) map[string]string {
	if existing == nil {
		existing = map[string]string{}
	}
	for key, value := range next {
		existing[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return existing
}

func (m *Manager) loginAWS(ctx context.Context, request LoginRequest) (LoginResponse, error) {
	snapshot, sources, err := m.discoverAWS()
	if err != nil {
		return LoginResponse{}, err
	}
	if request.ExpectedRevision == "" || request.ExpectedRevision != snapshot.Revision {
		return LoginResponse{}, ErrSnapshotChanged
	}
	ids := uniqueSorted(request.CandidateIDs)
	if len(ids) == 0 {
		return LoginResponse{}, fmt.Errorf("select at least one AWS SSO profile")
	}

	sessions := map[string]struct{}{}
	legacyRepresentatives := map[string]string{}
	selectedSources := make([]awsSource, 0, len(ids))
	for _, id := range ids {
		source, ok := sources[id]
		if !ok {
			return LoginResponse{}, fmt.Errorf("selected AWS SSO candidate is no longer available")
		}
		if !source.Candidate.CanApply {
			return LoginResponse{}, fmt.Errorf("selected AWS SSO profile is incomplete")
		}
		selectedSources = append(selectedSources, source)
		if source.Candidate.AuthMode == "sso-session" {
			sessions[source.SessionName] = struct{}{}
			continue
		}
		group := source.PortalHash + "\x00" + source.SSORegion
		if current, ok := legacyRepresentatives[group]; !ok || source.Candidate.SourceProfile < current {
			legacyRepresentatives[group] = source.Candidate.SourceProfile
		}
	}

	tempRoot, err := os.MkdirTemp("", "kervik-aws-sso-")
	if err != nil {
		return LoginResponse{}, fmt.Errorf("create isolated AWS SSO configuration: %w", err)
	}
	defer func() { _ = m.removeAll(tempRoot) }()
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		return LoginResponse{}, fmt.Errorf("secure isolated AWS SSO configuration: %w", err)
	}
	sanitizedConfigPath := filepath.Join(tempRoot, "config")
	sanitizedCredentialsPath := filepath.Join(tempRoot, "credentials")
	if err := os.WriteFile(sanitizedConfigPath, renderSanitizedAWSConfig(selectedSources), 0o600); err != nil {
		return LoginResponse{}, fmt.Errorf("write isolated AWS SSO configuration: %w", err)
	}
	if err := os.WriteFile(sanitizedCredentialsPath, []byte{}, 0o600); err != nil {
		return LoginResponse{}, fmt.Errorf("write isolated AWS credentials file: %w", err)
	}

	env := vendorEnvironment(map[string]string{
		"AWS_CLI_AUTO_PROMPT":         "off",
		"AWS_CONFIG_FILE":             sanitizedConfigPath,
		"AWS_EC2_METADATA_DISABLED":   "true",
		"AWS_PAGER":                   "",
		"AWS_SHARED_CREDENTIALS_FILE": sanitizedCredentialsPath,
	})
	sessionNames := make([]string, 0, len(sessions))
	for session := range sessions {
		sessionNames = append(sessionNames, session)
	}
	sort.Strings(sessionNames)
	for _, session := range sessionNames {
		if _, err := m.runner.Run(ctx, "aws", []string{"sso", "login", "--sso-session", session}, env); err != nil {
			return LoginResponse{}, fmt.Errorf("AWS SSO sign-in failed: %w", err)
		}
	}
	legacyProfiles := make([]string, 0, len(legacyRepresentatives))
	for _, profileName := range legacyRepresentatives {
		legacyProfiles = append(legacyProfiles, profileName)
	}
	sort.Strings(legacyProfiles)
	for _, profileName := range legacyProfiles {
		if _, err := m.runner.Run(ctx, "aws", []string{"sso", "login", "--profile", profileName}, env); err != nil {
			return LoginResponse{}, fmt.Errorf("AWS SSO sign-in failed: %w", err)
		}
	}
	// Login success only establishes a portal session. Verify each selected
	// profile resolves to the account declared in its sanitized SSO metadata;
	// otherwise never return it as a safe connection candidate.
	sort.Slice(selectedSources, func(i, j int) bool {
		return selectedSources[i].Candidate.SourceProfile < selectedSources[j].Candidate.SourceProfile
	})
	for _, source := range selectedSources {
		out, err := m.runner.Run(ctx, "aws", []string{
			"sts", "get-caller-identity",
			"--profile", source.Candidate.SourceProfile,
			"--output", "json",
			"--no-cli-pager",
		}, env)
		if err != nil {
			return LoginResponse{}, fmt.Errorf("AWS SSO identity verification failed: %w", err)
		}
		var identity awsSTSIdentity
		if err := json.Unmarshal(out, &identity); err != nil || !awsAccountIDPattern.MatchString(identity.Account) {
			return LoginResponse{}, fmt.Errorf("AWS SSO identity verification returned an invalid response")
		}
		if identity.Account != source.Candidate.AccountID {
			return LoginResponse{}, fmt.Errorf("AWS SSO identity does not match the selected account")
		}
	}

	if err := m.removeAll(tempRoot); err != nil {
		return LoginResponse{}, fmt.Errorf("remove isolated AWS SSO configuration: %w", err)
	}
	fresh, _, err := m.discoverAWS()
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{Provider: "aws", OK: true, LoggedIn: len(ids), Snapshot: fresh}, nil
}

func renderSanitizedAWSConfig(sources []awsSource) []byte {
	model := inifile.Model{}
	bySession := map[string]awsSource{}
	byProfile := map[string]awsSource{}
	for _, source := range sources {
		byProfile[source.Candidate.SourceProfile] = source
		if source.Candidate.AuthMode == "sso-session" {
			bySession[source.SessionName] = source
		}
	}
	profileNames := make([]string, 0, len(byProfile))
	for name := range byProfile {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, name := range profileNames {
		source := byProfile[name]
		fields := map[string]string{
			"sso_account_id": source.Candidate.AccountID,
			"sso_role_name":  source.Candidate.RoleName,
		}
		if source.Candidate.Region != "" {
			fields["region"] = source.Candidate.Region
		}
		if source.Candidate.AuthMode == "sso-session" {
			fields["sso_session"] = source.SessionName
		} else {
			fields["sso_start_url"] = source.StartURL
			fields["sso_region"] = source.SSORegion
		}
		sectionName := "profile " + name
		if name == "default" {
			sectionName = "default"
		}
		section := inifile.Section{Name: sectionName}
		section.ApplyFields(fields)
		model.Sections = append(model.Sections, section)
	}
	sessionNames := make([]string, 0, len(bySession))
	for name := range bySession {
		sessionNames = append(sessionNames, name)
	}
	sort.Strings(sessionNames)
	for _, name := range sessionNames {
		source := bySession[name]
		section := inifile.Section{Name: "sso-session " + name}
		section.ApplyFields(map[string]string{
			"sso_start_url":           source.StartURL,
			"sso_region":              source.SSORegion,
			"sso_registration_scopes": "sso:account:access",
		})
		model.Sections = append(model.Sections, section)
	}
	return inifile.Render(model)
}

func (m *Manager) applyAWS(request ApplyRequest) (ApplyResponse, error) {
	if request.Mode != ModeSelected {
		return ApplyResponse{}, fmt.Errorf("AWS sign-in sync accepts selected mode only")
	}
	snapshot, sources, err := m.discoverAWS()
	if err != nil {
		return ApplyResponse{}, err
	}
	if request.ExpectedRevision == "" || request.ExpectedRevision != snapshot.Revision {
		return ApplyResponse{}, ErrSnapshotChanged
	}
	ids := uniqueSorted(request.CandidateIDs)
	results := make([]ApplyResult, 0, len(ids))
	for _, id := range ids {
		source, ok := sources[id]
		if !ok || !source.Candidate.CanApply {
			return ApplyResponse{}, fmt.Errorf("selected AWS SSO candidate is unavailable or incomplete")
		}
		results = append(results, ApplyResult{
			CandidateID: id,
			Name:        source.Candidate.SourceProfile,
			Action:      "linked",
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return ApplyResponse{
		Provider: "aws", Revision: snapshot.Revision, Results: results, Added: len(results),
	}, nil
}
