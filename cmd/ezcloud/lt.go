package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"ez-cloud-manager/internal/awscreds"
	"ez-cloud-manager/internal/awslt"
	profilemodel "ez-cloud-manager/internal/profile"
	"ez-cloud-manager/internal/provider/awsprovider"
)

// ltCommand exposes EC2 Launch Template operations. The app's editing model
// is deliberately clone-edit-apply: a new version is created from a source
// version and (optionally) made default; the source is never mutated, so the
// previous state always exists to roll back to. Deleting versions is its own
// explicit subcommand — never a side effect.
func ltCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud lt templates|versions|get|apply|set-default|delete-versions --workspace ID --profile AWS_PROFILE …"))
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("lt "+sub, flag.ExitOnError)
	workspaceID := fs.String("workspace", "", "Workspace profile id")
	awsProfile := fs.String("profile", "", "AWS profile name")
	region := fs.String("region", "", "AWS region")
	name := fs.String("name", "", "launch template name")
	version := fs.String("version", "", "template version number")
	versions := fs.String("versions", "", "comma-separated version numbers")
	_ = fs.Parse(rest)

	if *workspaceID == "" || *awsProfile == "" || *region == "" {
		fail(fmt.Errorf("--workspace, --profile and --region are required"))
	}
	profileRoot, err := profilemodel.DefaultRoot()
	if err != nil {
		fail(err)
	}
	var client awslt.Client
	var cleanup func() error
	err = profilemodel.WithConnectionPolicyLock(profileRoot, func() error {
		workspaceProfile, authorizeErr := authorizeLTConnection(profileRoot, *workspaceID, *awsProfile)
		if authorizeErr != nil {
			return authorizeErr
		}
		if verifyErr := verifyLTWorkspaceEnvironment(workspaceProfile); verifyErr != nil {
			return verifyErr
		}
		// The policy lock stays held until a private sanitized snapshot of the
		// authorized identity exists. App-owned delete/recreate operations use
		// the same lock, so none can substitute a new identity in this gap.
		var snapshotErr error
		client, cleanup, snapshotErr = prepareLTClient(*awsProfile, *region)
		if snapshotErr != nil {
			return fmt.Errorf("isolate AWS connection for Launch Templates: %w", snapshotErr)
		}
		return nil
	})
	if err != nil {
		if cleanup != nil {
			_ = cleanup()
		}
		fail(err)
	}
	err = executeLTWithCleanup(func() error {
		return runLTSubcommand(
			sub, client, *awsProfile, *region, *name, *version, *versions,
		)
	}, cleanup)
	if err != nil {
		fail(err)
	}
}

func executeLTWithCleanup(run func() error, cleanup func() error) error {
	commandErr := run()
	cleanupErr := cleanup()
	if commandErr != nil && cleanupErr != nil {
		return fmt.Errorf("%w; also remove isolated AWS snapshot: %v", commandErr, cleanupErr)
	}
	if commandErr != nil {
		return commandErr
	}
	if cleanupErr != nil {
		return fmt.Errorf("remove isolated AWS snapshot: %w", cleanupErr)
	}
	return nil
}

func runLTSubcommand(sub string, client awslt.Client, awsProfile, region, name, version, versions string) error {
	requireName := func() (string, error) {
		if name == "" {
			return "", fmt.Errorf("--name is required")
		}
		return name, nil
	}
	switch sub {
	case "templates":
		templates, err := client.ListTemplates()
		if err != nil {
			return err
		}
		out := make([]ltTemplateJSON, len(templates))
		for i, t := range templates {
			out[i] = ltTemplateJSON{t.ID, t.Name, t.DefaultVersion, t.LatestVersion, t.CreatedBy, t.CreateTime}
		}
		return writeLTJSON(out)
	case "versions":
		templateName, err := requireName()
		if err != nil {
			return err
		}
		list, err := client.ListVersions(templateName)
		if err != nil {
			return err
		}
		out := make([]ltVersionJSON, len(list))
		for i, v := range list {
			out[i] = ltVersionJSON{v.Number, v.Description, v.IsDefault, v.CreateTime}
		}
		return writeLTJSON(out)
	case "get":
		if version == "" {
			return fmt.Errorf("--version is required")
		}
		templateName, err := requireName()
		if err != nil {
			return err
		}
		_, flat, err := client.GetVersionData(templateName, version)
		if err != nil {
			return err
		}
		return writeLTJSON(struct {
			Fields map[string]string `json:"fields"`
		}{Fields: flat})
	case "apply":
		templateName, err := requireName()
		if err != nil {
			return err
		}
		var req struct {
			SourceVersion string            `json:"sourceVersion"`
			Description   string            `json:"description"`
			SetDefault    bool              `json:"setDefault"`
			Fields        map[string]string `json:"fields"`
		}
		if err := decodeLimitedJSON(os.Stdin, &req); err != nil {
			return fmt.Errorf("read apply json: %w", err)
		}
		if len(req.Fields) == 0 {
			return fmt.Errorf("no edits to apply")
		}
		newVersion, err := client.CreateVersion(templateName, req.SourceVersion, req.Fields, req.Description)
		if err != nil {
			return err
		}
		if req.SetDefault {
			if err := client.SetDefaultVersion(templateName, fmt.Sprint(newVersion)); err != nil {
				// The version WAS created; report that alongside the failure
				// so the user does not re-apply and duplicate it.
				return fmt.Errorf("created version %d, but setting it default failed: %w", newVersion, err)
			}
		}
		editedKeys := make([]string, 0, len(req.Fields))
		for key := range req.Fields {
			editedKeys = append(editedKeys, key)
		}
		auditRecordKeys("lt-apply", "aws",
			fmt.Sprintf("%s@%s %s (v%s → v%d)", awsProfile, region, templateName, req.SourceVersion, newVersion),
			editedKeys)
		return writeLTJSON(struct {
			NewVersion int64 `json:"newVersion"`
			SetDefault bool  `json:"setDefault"`
		}{NewVersion: newVersion, SetDefault: req.SetDefault})
	case "set-default":
		if version == "" {
			return fmt.Errorf("--version is required")
		}
		templateName, err := requireName()
		if err != nil {
			return err
		}
		if err := client.SetDefaultVersion(templateName, version); err != nil {
			return err
		}
		auditRecord("lt-set-default", "aws", awsProfile, nil, nil)
		return writeLTJSON(okResponse{OK: true})
	case "delete-versions":
		if versions == "" {
			return fmt.Errorf("--versions is required (comma-separated)")
		}
		templateName, err := requireName()
		if err != nil {
			return err
		}
		list := strings.Split(versions, ",")
		for i := range list {
			list[i] = strings.TrimSpace(list[i])
		}
		if err := client.DeleteVersions(templateName, list); err != nil {
			return err
		}
		auditRecord("lt-delete-versions", "aws", awsProfile, nil, nil)
		return writeLTJSON(okResponse{OK: true})
	default:
		return fmt.Errorf("unknown lt subcommand %q", sub)
	}
}

func writeLTJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func prepareLTClient(profileName, region string) (awslt.Client, func() error, error) {
	credentialsPath, err := awscreds.DefaultPath()
	if err != nil {
		return awslt.Client{}, nil, err
	}
	configPath, err := awscreds.DefaultConfigPath()
	if err != nil {
		return awslt.Client{}, nil, err
	}
	snapshot, err := awsprovider.PrepareExecutionSnapshot(credentialsPath, configPath, profileName)
	if err != nil {
		return awslt.Client{}, nil, err
	}
	return awslt.Client{
		Profile:     profileName,
		Region:      region,
		Environment: snapshot.VendorOverrides(),
	}, snapshot.Close, nil
}

// authorizeLTConnection is the single policy gate shared by every Launch
// Templates subcommand. Keeping it separate makes the allow path testable
// without starting the AWS CLI or touching the network.
func authorizeLTConnection(root, workspaceID, awsProfile string) (profilemodel.Profile, error) {
	workspaceProfile, err := profilemodel.Get(root, workspaceID)
	if err != nil {
		return profilemodel.Profile{}, fmt.Errorf("load workspace: %w", err)
	}
	connection := profilemodel.AccountRef{Provider: "aws", Account: awsProfile}
	if !profilemodel.AllowsConnection(workspaceProfile, connection) {
		return profilemodel.Profile{}, fmt.Errorf(
			"AWS connection %q is not allowed in workspace %q",
			awsProfile,
			workspaceProfile.Name,
		)
	}
	return workspaceProfile, nil
}

// verifyLTWorkspaceEnvironment binds the policy decision above to the same
// AWS stores the vendor command will actually use. The native shell launches
// the core with exactly the owning Workspace's validated env; a stale window
// or a direct caller that supplies a different credentials/config path must
// be rejected before aws is resolved or executed.
func verifyLTWorkspaceEnvironment(workspace profilemodel.Profile) error {
	expected := map[string]string{
		"AWS_CONFIG_FILE":             "",
		"AWS_SHARED_CREDENTIALS_FILE": "",
	}
	for _, variable := range workspace.EnvVars {
		if _, tracked := expected[variable.Key]; tracked {
			expected[variable.Key] = strings.TrimSpace(variable.Value)
		}
	}
	for key, want := range expected {
		got := strings.TrimSpace(os.Getenv(key))
		if got != want {
			return fmt.Errorf(
				"workspace AWS context changed before execution (%s mismatch); reopen Launch Templates and retry",
				key,
			)
		}
	}
	return nil
}

type ltTemplateJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DefaultVersion int64  `json:"defaultVersion"`
	LatestVersion  int64  `json:"latestVersion"`
	CreatedBy      string `json:"createdBy,omitempty"`
	CreateTime     string `json:"createTime,omitempty"`
}

type ltVersionJSON struct {
	Number      int64  `json:"number"`
	Description string `json:"description,omitempty"`
	IsDefault   bool   `json:"isDefault"`
	CreateTime  string `json:"createTime,omitempty"`
}
