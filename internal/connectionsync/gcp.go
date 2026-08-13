package connectionsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"ez-cloud-manager/internal/gcpcreds"
)

var gcloudConfigNamePattern = regexp.MustCompile(`^[a-z][-a-z0-9]*$`)
var gcloudProjectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

type gcloudAccount struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}

type gcloudProject struct {
	ProjectID      string `json:"projectId"`
	Name           string `json:"name"`
	LifecycleState string `json:"lifecycleState"`
}

type gcpCandidateState struct {
	Candidate    Candidate
	Desired      map[string]string
	Expected     map[string]string
	ExpectAbsent bool
}

type gcpRevisionEntry struct {
	Candidate Candidate         `json:"candidate"`
	Baseline  map[string]string `json:"baseline,omitempty"`
}

type gcpDiscoveryState struct {
	Snapshot   DiscoverySnapshot
	Candidates map[string]gcpCandidateState
}

type gcpBatchConditionalSaver interface {
	SaveBatchIfUnchanged(path string, changes []gcpcreds.ConditionalSave) error
}

func (m *Manager) discoverGCP(ctx context.Context, requestedPrincipal, scratchConfig string) (snapshot DiscoverySnapshot, state *gcpDiscoveryState, resultErr error) {
	env := vendorEnvironment(map[string]string{"CLOUDSDK_CONFIG": m.gcpConfigRoot})
	principal := strings.TrimSpace(requestedPrincipal)
	if scratchConfig != "" {
		if !safeIdentifier(scratchConfig, 128) || !gcloudConfigNamePattern.MatchString(scratchConfig) {
			return DiscoverySnapshot{}, nil, fmt.Errorf("invalid scratch gcloud configuration name")
		}
		out, err := m.runner.Run(ctx, "gcloud", []string{
			"config", "get", "core/account", "--configuration=" + scratchConfig,
		}, env)
		if err != nil {
			return DiscoverySnapshot{}, nil, fmt.Errorf("read signed-in Google identity: %w", err)
		}
		principal = strings.TrimSpace(string(out))
		if !validGCPPrincipal(principal) || principal == "(unset)" {
			return DiscoverySnapshot{}, nil, fmt.Errorf("gcloud returned an invalid signed-in identity")
		}
	} else {
		resolved, err := m.resolveGCPPrincipal(ctx, principal, env)
		if err != nil {
			return DiscoverySnapshot{}, nil, err
		}
		principal = resolved
		scratchConfig, err = m.createGCPScratch(ctx, env)
		if err != nil {
			return DiscoverySnapshot{}, nil, err
		}
		defer func() {
			resultErr = mergeGCPCleanupError(resultErr, m.deleteGCPScratch(ctx, scratchConfig, env))
		}()
		if _, err := m.runner.Run(ctx, "gcloud", []string{
			"config", "set", "core/account", principal,
			"--configuration=" + scratchConfig, "--quiet",
		}, env); err != nil {
			return DiscoverySnapshot{}, nil, fmt.Errorf("prepare isolated Google Cloud discovery: %w", err)
		}
	}

	projectArgs := []string{
		"projects", "list", "--configuration=" + scratchConfig,
		"--filter=lifecycleState:ACTIVE",
		"--format=json(projectId,name,lifecycleState)",
	}
	out, err := m.runner.Run(ctx, "gcloud", projectArgs, env)
	if err != nil {
		return DiscoverySnapshot{}, nil, fmt.Errorf("discover Google Cloud projects: %w", err)
	}
	projects := []gcloudProject{}
	if err := decodeJSONArray(out, &projects); err != nil {
		return DiscoverySnapshot{}, nil, fmt.Errorf("decode Google Cloud projects: %w", err)
	}
	if len(projects) > maxGCPProjects {
		return DiscoverySnapshot{}, nil, fmt.Errorf("gcloud returned more than %d projects", maxGCPProjects)
	}
	state, err = m.buildGCPSnapshot(principal, projects)
	if err != nil {
		return DiscoverySnapshot{}, nil, err
	}
	return state.Snapshot, state, nil
}

func (m *Manager) createGCPScratch(ctx context.Context, env []string) (string, error) {
	nonce, err := m.nonce()
	if err != nil {
		return "", fmt.Errorf("create scratch gcloud configuration name: %w", err)
	}
	scratch := "kervik-auth-" + strings.ToLower(nonce)
	if !gcloudConfigNamePattern.MatchString(scratch) || len(scratch) > 128 {
		return "", fmt.Errorf("generated scratch gcloud configuration name is invalid")
	}
	if _, err := m.runner.Run(ctx, "gcloud", []string{
		"config", "configurations", "create", scratch, "--no-activate", "--quiet",
	}, env); err != nil {
		return "", fmt.Errorf("create scratch gcloud configuration: %w", err)
	}
	return scratch, nil
}

func (m *Manager) deleteGCPScratch(ctx context.Context, scratch string, env []string) error {
	cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancelCleanup()
	if _, err := m.runner.Run(cleanupContext, "gcloud", []string{
		"config", "configurations", "delete", scratch, "--quiet",
	}, env); err != nil {
		return fmt.Errorf("delete scratch gcloud configuration: %w", err)
	}
	return nil
}

func mergeGCPCleanupError(operationErr, cleanupErr error) error {
	if cleanupErr == nil {
		return operationErr
	}
	if operationErr == nil {
		return cleanupErr
	}
	// Vendor errors are intentionally terse and cleanup failure must not mask
	// the operation's actionable cancellation/network/auth result.
	if errors.Is(cleanupErr, errVendorCLI) {
		return operationErr
	}
	return errors.Join(operationErr, cleanupErr)
}

func (m *Manager) resolveGCPPrincipal(ctx context.Context, requested string, env []string) (string, error) {
	args := []string{"auth", "list", "--format=json(account,status)"}
	if requested == "" {
		args = []string{"auth", "list", "--filter=status:ACTIVE", "--format=json(account,status)"}
	} else if !validGCPPrincipal(requested) {
		return "", fmt.Errorf("invalid Google identity")
	}
	out, err := m.runner.Run(ctx, "gcloud", args, env)
	if err != nil {
		return "", fmt.Errorf("inspect gcloud identities: %w", err)
	}
	accounts := []gcloudAccount{}
	if err := decodeJSONArray(out, &accounts); err != nil {
		return "", fmt.Errorf("decode gcloud identities: %w", err)
	}
	if len(accounts) > 1000 {
		return "", fmt.Errorf("gcloud returned too many identities")
	}
	if requested != "" {
		for _, account := range accounts {
			if account.Account == requested {
				return requested, nil
			}
		}
		return "", fmt.Errorf("selected Google identity is no longer credentialed locally")
	}
	active := ""
	for _, account := range accounts {
		if strings.EqualFold(strings.TrimSpace(account.Status), "ACTIVE") {
			if active != "" && active != account.Account {
				return "", fmt.Errorf("gcloud reported multiple active identities")
			}
			active = strings.TrimSpace(account.Account)
		}
	}
	if !validGCPPrincipal(active) {
		return "", fmt.Errorf("no active gcloud identity; sign in first")
	}
	return active, nil
}

func decodeJSONArray(data []byte, destination any) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		data = []byte("[]")
	}
	return json.Unmarshal(data, destination)
}

func validGCPPrincipal(value string) bool {
	return safeIdentifier(value, 1024) && !strings.HasPrefix(strings.TrimSpace(value), "-")
}

func (m *Manager) buildGCPSnapshot(principal string, projects []gcloudProject) (*gcpDiscoveryState, error) {
	if !validGCPPrincipal(principal) {
		return nil, fmt.Errorf("invalid Google identity")
	}
	summaries, err := m.gcpStore.List(m.gcpConfigRoot)
	if err != nil {
		return nil, fmt.Errorf("list gcloud configurations: %w", err)
	}
	existing := make(map[string]bool, len(summaries))
	for _, summary := range summaries {
		existing[summary.Name] = true
	}

	projectByID := make(map[string]gcloudProject, len(projects))
	warnings := []string{}
	for _, project := range projects {
		project.ProjectID = strings.TrimSpace(project.ProjectID)
		if !gcloudProjectIDPattern.MatchString(project.ProjectID) {
			warnings = append(warnings, "Ignored a Google Cloud project with an invalid identifier.")
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(project.LifecycleState), "ACTIVE") && strings.TrimSpace(project.LifecycleState) != "" {
			continue
		}
		if _, duplicate := projectByID[project.ProjectID]; duplicate {
			return nil, fmt.Errorf("gcloud returned a duplicate Google Cloud project identifier")
		}
		projectByID[project.ProjectID] = project
	}
	projectIDs := make([]string, 0, len(projectByID))
	for projectID := range projectByID {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Strings(projectIDs)

	states := make(map[string]gcpCandidateState, len(projectIDs))
	entries := make([]gcpRevisionEntry, 0, len(projectIDs))
	usedNames := map[string]string{}
	for _, projectID := range projectIDs {
		project := projectByID[projectID]
		configName := gcpConfigurationName(projectID)
		if previous, collision := usedNames[configName]; collision && previous != projectID {
			return nil, fmt.Errorf("Google project identifiers map to the same local configuration name")
		}
		usedNames[configName] = projectID
		displayName := strings.TrimSpace(project.Name)
		if !safeIdentifier(displayName, 512) {
			displayName = projectID
		}
		desired := map[string]string{
			gcpcreds.KeyAccount: principal,
			gcpcreds.KeyProject: projectID,
		}
		candidate := Candidate{
			ID:          stableID("gcp", principal, projectID),
			Name:        configName,
			DisplayName: displayName,
			AuthMode:    "gcloud",
			Principal:   principal,
			ProjectID:   projectID,
			Status:      StatusNew,
			CanApply:    true,
		}
		state := gcpCandidateState{Candidate: candidate, Desired: desired, ExpectAbsent: true}
		if existing[configName] {
			profile, err := m.gcpStore.Get(m.gcpConfigRoot, configName)
			if err != nil {
				return nil, fmt.Errorf("read an existing gcloud configuration: %w", err)
			}
			state.Expected = cloneFields(profile.Fields)
			state.ExpectAbsent = false
			if profile.Fields[gcpcreds.KeyAccount] == principal && profile.Fields[gcpcreds.KeyProject] == projectID {
				candidate.Status = StatusUnchanged
			} else {
				candidate.Status = StatusUpdate
			}
			if hasUnsafeGCPRoutingOverride(profile.Fields) {
				candidate.CanApply = false
				candidate.Reason = "Existing configuration contains credential or endpoint overrides; review it manually before replacing."
			}
			state.Candidate = candidate
		}
		states[candidate.ID] = state
		entries = append(entries, gcpRevisionEntry{Candidate: candidate, Baseline: state.Expected})
	}
	rev, err := revision(struct {
		Principal string             `json:"principal"`
		Entries   []gcpRevisionEntry `json:"entries"`
	}{Principal: principal, Entries: entries})
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		candidates = append(candidates, entry.Candidate)
	}
	snapshot := DiscoverySnapshot{
		ProtocolVersion: ProtocolVersion,
		Provider:        "gcp",
		Principal:       principal,
		Revision:        rev,
		Candidates:      candidates,
		Warnings:        warnings,
	}
	return &gcpDiscoveryState{Snapshot: snapshot, Candidates: states}, nil
}

func hasUnsafeGCPRoutingOverride(fields map[string]string) bool {
	for rawKey, rawValue := range fields {
		if strings.TrimSpace(rawValue) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(rawKey))
		for _, prefix := range []string{"auth.", "proxy.", "api_endpoint_overrides.", "context_aware."} {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		if key == "core.custom_ca_certs_file" || key == "core.universe_domain" {
			return true
		}
		// Fail closed for provider properties added after this release whose
		// names still clearly alter identity or transport routing.
		for _, marker := range []string{
			"credential", "access_token", "impersonat", "login_config",
			"endpoint", "custom_ca", "client_certificate",
		} {
			if strings.Contains(key, marker) {
				return true
			}
		}
	}
	return false
}

func gcpConfigurationName(projectID string) string {
	value := strings.ToLower(strings.TrimSpace(projectID))
	if gcloudConfigNamePattern.MatchString(value) {
		return value
	}
	return "project-" + strings.TrimPrefix(stableID("gcp-config", projectID), "sha256:")[:16]
}

func cloneFields(fields map[string]string) map[string]string {
	if fields == nil {
		return nil
	}
	out := make(map[string]string, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func (m *Manager) loginGCP(ctx context.Context) (response LoginResponse, resultErr error) {
	env := vendorEnvironment(map[string]string{"CLOUDSDK_CONFIG": m.gcpConfigRoot})
	scratch, err := m.createGCPScratch(ctx, env)
	if err != nil {
		return LoginResponse{}, err
	}
	defer func() {
		resultErr = mergeGCPCleanupError(resultErr, m.deleteGCPScratch(ctx, scratch, env))
	}()

	if _, err := m.runner.Run(ctx, "gcloud", []string{
		"auth", "login", "--configuration=" + scratch, "--brief",
	}, env); err != nil {
		return LoginResponse{}, fmt.Errorf("Google Cloud sign-in failed: %w", err)
	}
	snapshot, _, err := m.discoverGCP(ctx, "", scratch)
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{Provider: "gcp", OK: true, LoggedIn: 1, Snapshot: snapshot}, nil
}

func (m *Manager) applyGCP(ctx context.Context, request ApplyRequest) (ApplyResponse, error) {
	if request.ExpectedRevision == "" || !validGCPPrincipal(request.Principal) {
		return ApplyResponse{}, fmt.Errorf("expectedRevision and principal are required")
	}
	if request.Mode != ModeSelected && request.Mode != ModeUpdateAll && request.Mode != ModeAddNew {
		return ApplyResponse{}, fmt.Errorf("invalid sync mode %q", request.Mode)
	}
	snapshot, state, err := m.discoverGCP(ctx, request.Principal, "")
	if err != nil {
		return ApplyResponse{}, err
	}
	if snapshot.Revision != request.ExpectedRevision {
		return ApplyResponse{}, ErrSnapshotChanged
	}
	batchSaver, ok := m.gcpStore.(gcpBatchConditionalSaver)
	if !ok {
		return ApplyResponse{}, fmt.Errorf("GCP provider does not support atomic conditional synchronization")
	}

	selected := map[string]bool{}
	if request.Mode == ModeSelected {
		for _, id := range uniqueSorted(request.CandidateIDs) {
			if _, ok := state.Candidates[id]; !ok {
				return ApplyResponse{}, fmt.Errorf("selected Google Cloud project is no longer available")
			}
			selected[id] = true
		}
	} else {
		for id, candidateState := range state.Candidates {
			if !candidateState.Candidate.CanApply {
				continue
			}
			if request.Mode == ModeAddNew && candidateState.Candidate.Status == StatusNew {
				selected[id] = true
			}
			if request.Mode == ModeUpdateAll && candidateState.Candidate.Status != StatusNew {
				selected[id] = true
			}
		}
	}

	results := []ApplyResult{}
	response := ApplyResponse{Provider: "gcp", Revision: snapshot.Revision, Results: results}
	changes := []gcpcreds.ConditionalSave{}
	for _, candidate := range snapshot.Candidates {
		if !selected[candidate.ID] {
			continue
		}
		candidateState := state.Candidates[candidate.ID]
		if !candidate.CanApply {
			return ApplyResponse{}, fmt.Errorf("selected Google Cloud configuration requires manual review")
		}
		if candidate.Status == StatusUnchanged {
			response.Results = append(response.Results, ApplyResult{
				CandidateID: candidate.ID, Name: candidate.Name, Action: "unchanged",
			})
			response.Unchanged++
			continue
		}
		changes = append(changes, gcpcreds.ConditionalSave{
			Name: candidate.Name, Fields: candidateState.Desired,
			ExpectedFields: candidateState.Expected, ExpectAbsent: candidateState.ExpectAbsent,
		})
		action := "updated"
		if candidate.Status == StatusNew {
			action = "added"
			response.Added++
		} else {
			response.Updated++
		}
		response.Results = append(response.Results, ApplyResult{
			CandidateID: candidate.ID, Name: candidate.Name, Action: action,
		})
	}
	if err := batchSaver.SaveBatchIfUnchanged(m.gcpConfigRoot, changes); err != nil {
		return ApplyResponse{}, fmt.Errorf("sync gcloud configurations: %w", err)
	}
	return response, nil
}
