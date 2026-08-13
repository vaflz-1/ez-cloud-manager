// Command ezcloud is the JSON-speaking core behind the EZ Cloud Manager UI
// and a standalone CLI. Every command addresses one provider backend
// (--provider aws|gcp|azure, default aws so existing scripts and the AWS-only
// UI contract keep working unchanged).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ez-cloud-manager/internal/export"
	profilemodel "ez-cloud-manager/internal/profile"
	"ez-cloud-manager/internal/provider"
	_ "ez-cloud-manager/internal/provider/awsprovider"   // register "aws"
	_ "ez-cloud-manager/internal/provider/azureprovider" // register "azure"
	_ "ez-cloud-manager/internal/provider/gcpprovider"   // register "gcp"
)

// defaultProvider preserves the pre-multi-cloud CLI/UI contract.
const defaultProvider = "aws"

const connectionDeletedCleanupFailedMarker = "EZCLOUD_CONNECTION_DELETED_SCOPE_CLEANUP_FAILED"
const connectionIdentityChangeMarker = "EZCLOUD_CONNECTION_IDENTITY_CHANGE_REQUIRES_NEW_NAME"

type listResponse struct {
	Provider string                    `json:"provider"`
	Path     string                    `json:"path"`
	Profiles []provider.ProfileSummary `json:"profiles"`
}

type saveRequest struct {
	Fields         map[string]string `json:"fields"`
	ExpectedFields map[string]string `json:"expectedFields,omitempty"`
	ExpectAbsent   bool              `json:"expectAbsent,omitempty"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

type providerInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Icon        string `json:"icon"`
	// CanActivate tells the UI whether to offer a "make active/default"
	// action for this provider's profiles.
	CanActivate   bool   `json:"canActivate"`
	ActivateLabel string `json:"activateLabel,omitempty"`
	// CanAuthenticate is derived from the embedded Connector capability
	// manifest. Native clients never hardcode which providers support an
	// explicit browser/device sign-in flow.
	CanAuthenticate bool `json:"canAuthenticate"`
	CanSync         bool `json:"canSync"`
}

// maxStdinBytes bounds how much we read from stdin (save JSON / parse blob).
// Config payloads are tiny; this caps memory against a huge/hostile pipe.
const maxStdinBytes = 4 << 20 // 4 MiB

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, rest := os.Args[1], os.Args[2:]

	switch cmd {
	case "app":
		appCommand(rest)
	case "connections":
		connectionsCommand(rest)
	case "providers":
		infos, err := registeredProviderInfos()
		if err != nil {
			fail(err)
		}
		writeJSON(infos)
	case "list", "get", "save", "delete", "parse", "schema", "export", "activate":
		profileCommand(cmd, rest)
	case "check":
		checkCommand(rest)
	case "ws":
		wsCommand(rest)
	case "profile":
		profileMgmtCommand(rest)
	case "audit":
		auditCommand(rest)
	case "lt":
		ltCommand(rest)
	case "plugins":
		pluginsCommand(rest)
	default:
		usage()
		os.Exit(2)
	}
}

// profileCommand runs the per-profile operations against one provider.
func profileCommand(cmd string, args []string) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	providerID := fs.String("provider", defaultProvider, "provider id (aws, gcp, azure)")
	profile := fs.String("profile", "", "profile name")
	workspaceID := fs.String("workspace", "", "owning Workspace profile id")
	format := fs.String("format", "env", "export format: env, dotenv, ini, json")
	_ = fs.Parse(args)

	prov, err := provider.Get(*providerID)
	if err != nil {
		fail(err)
	}
	requireProfile := func() string {
		if *profile == "" {
			fail(fmt.Errorf("--profile is required"))
		}
		return *profile
	}

	switch cmd {
	case "list":
		path, err := prov.DefaultPath()
		if err != nil {
			fail(err)
		}
		profiles, err := prov.List(path)
		if err != nil {
			fail(err)
		}
		writeJSON(listResponse{Provider: prov.ID(), Path: path, Profiles: profiles})
	case "get":
		name := requireProfile()
		var p provider.Profile
		err := withWorkspaceConnectionOperation(*workspaceID, prov.ID(), name, false, func(operation workspaceConnectionOperation) error {
			var getErr error
			p, getErr = operation.Provider.Get(operation.Path, name)
			return getErr
		})
		if err != nil {
			fail(err)
		}
		writeJSON(p)
	case "save":
		name := requireProfile()
		var req saveRequest
		if err := decodeLimitedJSON(os.Stdin, &req); err != nil {
			fail(fmt.Errorf("read save json: %w", err))
		}
		oldFields := req.ExpectedFields
		err := withWorkspaceConnectionOperation(*workspaceID, prov.ID(), name, true, func(operation workspaceConnectionOperation) error {
			if operation.Exists {
				current, getErr := operation.Provider.Get(operation.Path, name)
				if getErr != nil {
					return getErr
				}
				if oldFields == nil {
					oldFields = current.Fields
				}
				if connectionMaterialIdentityChanged(operation.Provider.ID(), current.Fields, req.Fields) {
					return fmt.Errorf(
						"%s: connection %q changes its material identity; create a new named Connection and review Workspace access instead",
						connectionIdentityChangeMarker, name,
					)
				}
			}
			if !operation.Exists {
				// A name deleted in the past must never inherit old Workspace
				// grants when a new identity is created under the same name.
				if cleanupErr := removeConnectionRefsFromMatchingWorkspacesWithPolicyLockHeld(
					operation.Root, operation.Provider.ID(), name, operation.Path,
				); cleanupErr != nil {
					return fmt.Errorf("refusing to create connection until stale workspace grants are removed: %w", cleanupErr)
				}
			}
			if req.ExpectedFields != nil || req.ExpectAbsent {
				conditional, ok := operation.Provider.(provider.ConditionalSaver)
				if !ok {
					return fmt.Errorf("provider %q does not support conditional saves", operation.Provider.ID())
				}
				return conditional.SaveIfUnchanged(operation.Path, name, req.Fields, req.ExpectedFields, req.ExpectAbsent)
			}
			return operation.Provider.Save(operation.Path, name, req.Fields)
		})
		if err != nil {
			fail(err)
		}
		auditRecord("save", prov.ID(), name, oldFields, req.Fields)
		writeJSON(okResponse{OK: true})
	case "delete":
		name := requireProfile()
		var old provider.Profile
		deleted := false
		err := withWorkspaceConnectionOperation(*workspaceID, prov.ID(), name, false, func(operation workspaceConnectionOperation) error {
			old, _ = operation.Provider.Get(operation.Path, name)
			if deleteErr := operation.Provider.Delete(operation.Path, name); deleteErr != nil {
				return deleteErr
			}
			deleted = true
			if cleanupErr := removeConnectionRefsFromMatchingWorkspacesWithPolicyLockHeld(
				operation.Root, operation.Provider.ID(), name, operation.Path,
			); cleanupErr != nil {
				return fmt.Errorf("%s: connection %q was deleted, but workspace grant cleanup failed: %w", connectionDeletedCleanupFailedMarker, name, cleanupErr)
			}
			return nil
		})
		if deleted {
			auditRecord("delete", prov.ID(), name, old.Fields, nil)
		}
		if err != nil {
			fail(err)
		}
		writeJSON(okResponse{OK: true})
	case "parse":
		data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
		if err != nil {
			fail(err)
		}
		writeJSON(prov.Parse(string(data)))
	case "schema":
		writeJSON(prov.Schema())
	case "export":
		name := requireProfile()
		var out string
		err := withWorkspaceConnectionOperation(*workspaceID, prov.ID(), name, false, func(operation workspaceConnectionOperation) error {
			p, getErr := operation.Provider.Get(operation.Path, name)
			if getErr != nil {
				return getErr
			}
			var renderErr error
			out, renderErr = export.Render(operation.Provider.Schema(), p.Name, p.Fields, *format)
			return renderErr
		})
		if err != nil {
			fail(err)
		}
		// Raw text on stdout (not JSON): exports are meant to be piped to
		// files/clipboard as-is.
		fmt.Print(out)
	case "activate":
		name := requireProfile()
		err := withWorkspaceConnectionOperation(*workspaceID, prov.ID(), name, false, func(operation workspaceConnectionOperation) error {
			act, ok := operation.Provider.(provider.Activator)
			if !ok {
				return fmt.Errorf("provider %q has no activate action", operation.Provider.ID())
			}
			return act.Activate(operation.Path, name)
		})
		if err != nil {
			fail(err)
		}
		auditRecord("activate", prov.ID(), name, nil, nil)
		writeJSON(okResponse{OK: true})
	}
}

// connectionMaterialIdentityChanged distinguishes ordinary secret rotation
// and presentation defaults from a new principal/target hidden behind an old
// name. A named grant is an identity reference, so changing any unclassified
// field is fail-closed and requires a new Connection name. This keeps another
// Workspace's existing {provider,name} grant from silently inheriting a
// different account, tenant, project or credential resolver.
func connectionMaterialIdentityChanged(providerID string, current, patch map[string]string) bool {
	effective := make(map[string]string, len(current)+len(patch))
	for key, value := range current {
		effective[key] = strings.TrimSpace(value)
	}
	for key, value := range patch {
		value = strings.TrimSpace(value)
		if value == "" {
			delete(effective, key)
		} else {
			effective[key] = value
		}
	}
	nonIdentity := map[string]bool{}
	switch providerID {
	case "aws":
		for _, key := range []string{
			"aws_secret_access_key", "aws_session_token", "region", "output",
			"duration_seconds", "cli_pager", "retry_mode", "max_attempts",
			"sts_regional_endpoints",
		} {
			nonIdentity[key] = true
		}
	case "gcp":
		for _, key := range []string{
			"compute.region", "compute.zone", "core.disable_usage_reporting",
		} {
			nonIdentity[key] = true
		}
	case "azure":
		for _, key := range []string{"client_secret", "location", "resource_group"} {
			nonIdentity[key] = true
		}
	}
	keys := make(map[string]bool, len(current)+len(effective))
	for key := range current {
		keys[key] = true
	}
	for key := range effective {
		keys[key] = true
	}
	for key := range keys {
		if nonIdentity[key] {
			continue
		}
		if strings.TrimSpace(current[key]) != strings.TrimSpace(effective[key]) {
			return true
		}
	}
	return false
}

type workspaceConnectionOperation struct {
	Root      string
	Workspace profilemodel.Profile
	Provider  provider.Provider
	Path      string
	Exists    bool
}

// withWorkspaceConnectionOperation is the core authorization boundary for
// every Connection read or mutation. The Workspace policy, provider routing
// environment, existence check and provider operation are linearized under
// the same cross-process root lock, so a delete/recreate cannot substitute a
// new identity between an authorization check and use.
func withWorkspaceConnectionOperation(
	workspaceID, providerID, account string,
	allowMissing bool,
	operation func(workspaceConnectionOperation) error,
) error {
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("--workspace is required for provider connection operations")
	}
	if operation == nil {
		return errors.New("workspace connection operation is required")
	}
	root, err := profilemodel.DefaultRoot()
	if err != nil {
		return err
	}
	return profilemodel.WithConnectionPolicyLock(root, func() error {
		workspace, err := profilemodel.Get(root, workspaceID)
		if err != nil {
			return fmt.Errorf("load workspace: %w", err)
		}
		restore, err := applyWorkspaceRoutingEnvironment(providerID, workspace)
		if err != nil {
			return err
		}
		defer restore()
		prov, err := provider.Get(providerID)
		if err != nil {
			return err
		}
		path, err := prov.DefaultPath()
		if err != nil {
			return err
		}
		exists, err := connectionExists(prov, path, account)
		if err != nil {
			return err
		}
		if exists && !profilemodel.AllowsConnection(workspace, profilemodel.AccountRef{Provider: providerID, Account: account}) {
			return fmt.Errorf("connection %q is not allowed in workspace %q", account, workspace.Name)
		}
		if !exists && !allowMissing {
			return fmt.Errorf("connection %q is not allowed or no longer exists in workspace %q", account, workspace.Name)
		}
		return operation(workspaceConnectionOperation{
			Root: root, Workspace: workspace, Provider: prov, Path: path, Exists: exists,
		})
	})
}

func connectionExists(prov provider.Provider, path, name string) (bool, error) {
	profiles, err := prov.List(path)
	if err != nil {
		return false, err
	}
	for _, candidate := range profiles {
		if candidate.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func removeConnectionRefsFromMatchingWorkspaces(providerID, account, deletedStorePath string) error {
	root, err := profilemodel.DefaultRoot()
	if err != nil {
		return err
	}
	return profilemodel.WithConnectionPolicyLock(root, func() error {
		return removeConnectionRefsFromMatchingWorkspacesWithPolicyLockHeld(
			root, providerID, account, deletedStorePath,
		)
	})
}

func removeConnectionRefsFromMatchingWorkspacesWithPolicyLockHeld(root, providerID, account, deletedStorePath string) error {
	deletedStoreIdentity, err := canonicalConnectionStorePath(deletedStorePath)
	if err != nil {
		return err
	}
	return profilemodel.RemoveConnectionRefFromMatchingWithPolicyLockHeld(root, profilemodel.AccountRef{
		Provider: providerID,
		Account:  account,
	}, func(workspace profilemodel.Profile) (bool, error) {
		workspacePath, supported, err := workspaceConnectionStorePath(providerID, workspace)
		if err != nil || !supported {
			return false, err
		}
		workspaceStoreIdentity, err := canonicalConnectionStorePath(workspacePath)
		if err != nil {
			return false, err
		}
		return workspaceStoreIdentity == deletedStoreIdentity, nil
	})
}

// workspaceConnectionStorePath resolves a Workspace's provider store without
// inheriting the current command's request-scoped provider override. The app
// launches ezcloud with one Workspace's EnvVars layered onto a minimal child
// environment; applying that override to every other Workspace is exactly how
// equal profile names in distinct AWS/GCP stores became cross-contaminated.
func workspaceConnectionStorePath(providerID string, workspace profilemodel.Profile) (string, bool, error) {
	env := make(map[string]string, len(workspace.EnvVars))
	for _, variable := range workspace.EnvVars {
		env[variable.Key] = strings.TrimSpace(variable.Value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	switch providerID {
	case "aws":
		if override := env["AWS_SHARED_CREDENTIALS_FILE"]; override != "" {
			return override, true, nil
		}
		return filepath.Join(home, ".aws", "credentials"), true, nil
	case "gcp":
		if override := env["CLOUDSDK_CONFIG"]; override != "" {
			return override, true, nil
		}
		return filepath.Join(home, ".config", "gcloud"), true, nil
	case "azure":
		// EZCLOUD_* variables are deliberately forbidden in Workspace EnvVars,
		// so Azure's app-owned store is shared and follows the command's trusted
		// ambient configuration root rather than this Workspace map.
		if override := strings.TrimSpace(os.Getenv("EZCLOUD_AZURE_PROFILES_FILE")); override != "" {
			return override, true, nil
		}
		if configDir := strings.TrimSpace(os.Getenv("EZCLOUD_CONFIG_DIR")); configDir != "" {
			return filepath.Join(configDir, "azure_profiles.ini"), true, nil
		}
		return filepath.Join(home, ".config", "ezcloud", "azure_profiles.ini"), true, nil
	default:
		// A future provider must define store identity before delete can safely
		// mutate Workspace grants. Skipping cleanup is safer than crossing stores.
		return "", false, nil
	}
}

func canonicalConnectionStorePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	// A store or several of its trailing directories may not exist yet (or the
	// Azure file may disappear after deleting its last entry). Resolve the
	// longest existing ancestor, then append the untouched suffix so symlinked
	// config roots still compare by filesystem identity.
	probe := abs
	suffix := []string{}
	for {
		if resolved, resolveErr := filepath.EvalSymlinks(probe); resolveErr == nil {
			parts := append([]string{filepath.Clean(resolved)}, suffix...)
			return filepath.Join(parts...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		suffix = append([]string{filepath.Base(probe)}, suffix...)
		probe = parent
	}
	return abs, nil
}

func decodeLimitedJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxStdinBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func writeJSON(value any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
	  ezcloud app bootstrap
	  ezcloud connections list
	  ezcloud connections auth discover --provider aws|gcp [--principal GCP_ACCOUNT]
	  ezcloud connections auth login    --provider aws|gcp < login.json
	  ezcloud connections auth apply    --provider aws|gcp < apply.json
	  ezcloud providers
  ezcloud list     [--provider ID]
  ezcloud get      [--provider ID] --workspace ID --profile NAME
  ezcloud save     [--provider ID] --workspace ID --profile NAME < save.json
  ezcloud delete   [--provider ID] --workspace ID --profile NAME
  ezcloud parse    [--provider ID] < pasted-credentials.txt
  ezcloud schema   [--provider ID]
  ezcloud export   [--provider ID] --workspace ID --profile NAME [--format env|dotenv|ini|json]
  ezcloud activate [--provider ID] --workspace ID --profile NAME
  ezcloud check    [--provider ID] --workspace ID --profile NAME [--timeout SECONDS]
  ezcloud ws       list | save | delete --name NAME | rename --old A --new B
  ezcloud profile  list|show|create|save|rename|duplicate|delete|export|import|migrate
  ezcloud profile  connections add|remove|authorize --id ID --provider ID --account NAME
  ezcloud profile  settings get|set --id ID --plugin PLUGIN_ID
  ezcloud audit    [--limit N]
  ezcloud lt       templates|versions|get|apply|set-default|delete-versions --workspace ID --profile AWS_PROFILE …
  ezcloud plugins  list [--profile ID] | update --profile ID < changes.json | enable|disable --profile ID --id ID

The default provider is "aws", preserving the original single-cloud behavior.`)
}
