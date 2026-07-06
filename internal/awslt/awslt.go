// Package awslt performs EC2 Launch Template operations by shelling out to the
// AWS CLI (the `aws` binary) rather than the AWS SDK. The design is local-first:
// nothing here touches the network until one of the Client methods runs, so the
// surrounding app can list, build, and validate UI state offline and only reach
// AWS on an explicit user action.
//
// Every operation is expressed as an `aws ec2 <subcommand> ...` invocation. The
// binary is executed directly (no shell), and secret-bearing data such as
// user-data is passed via a temp file, never on the command line, so it cannot
// leak through the process table or shell history.
package awslt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"ez-cloud-manager/internal/flatjson"
)

// Runner executes an `aws` invocation with the given argv (excluding the binary
// name) and optional stdin, returning stdout. Tests inject a fake Runner to
// assert argv construction without touching a real CLI or the network.
type Runner func(args []string, stdin []byte) ([]byte, error)

// errNoCLI is returned by any operation when the aws binary is not installed.
var errNoCLI = errors.New("AWS CLI (aws) not found in PATH — install it to use Launch Templates")

// aws binary resolution is done once. lookAWS is a variable so tests can force
// the "not found" path deterministically without depending on the host's PATH.
var (
	awsOnce sync.Once
	awsBin  string
	awsErr  error
	lookAWS = func() (string, error) { return exec.LookPath("aws") }
)

func resolveAWS() (string, error) {
	awsOnce.Do(func() { awsBin, awsErr = lookAWS() })
	return awsBin, awsErr
}

// CLIAvailable reports whether the aws binary can be found in PATH.
func CLIAvailable() bool {
	_, err := resolveAWS()
	return err == nil
}

// defaultRunner is the package-level Runner used when a Client leaves Run nil.
func defaultRunner(args []string, stdin []byte) ([]byte, error) {
	bin, err := resolveAWS()
	if err != nil {
		return nil, errNoCLI
	}
	return runAWS(bin, args, stdin)
}

// runAWS execs bin with args, returning stdout on success. On a non-zero exit it
// returns an error carrying the trimmed stderr so the CLI's own diagnostics
// (e.g. "An error occurred (InvalidLaunchTemplateName.NotFound) ...") surface.
func runAWS(bin string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("aws %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// Client targets one AWS profile and region. Run overrides the CLI executor in
// tests; when nil, defaultRunner is used.
type Client struct {
	Profile string
	Region  string
	Run     Runner
}

func (c Client) runner() Runner {
	if c.Run != nil {
		return c.Run
	}
	return defaultRunner
}

// Template is a summary row from describe-launch-templates.
type Template struct {
	ID             string
	Name           string
	DefaultVersion int64
	LatestVersion  int64
	CreatedBy      string
	CreateTime     string
}

// Version is one entry from describe-launch-template-versions.
type Version struct {
	Number      int64
	Description string
	IsDefault   bool
	CreateTime  string
}

// safeArgRe constrains profile and region to a conservative argv-safe set. They
// are passed as separate argv elements (so no shell is involved), but keeping
// them free of whitespace and shell/flag metacharacters is defense in depth.
var safeArgRe = regexp.MustCompile(`^[A-Za-z0-9_.:/=+@-]+$`)

// versionRe matches a plain version number; "$Latest" and "$Default" are the
// only other accepted forms.
var versionRe = regexp.MustCompile(`^[0-9]+$`)

// command assembles the full argv for an `aws ec2 <subcommand>` call after
// validating the profile and region. Op-specific flags come first, followed by
// the invariants every call shares.
func (c Client) command(subcommand string, opArgs []string) ([]string, error) {
	if err := validateArg("profile", c.Profile); err != nil {
		return nil, err
	}
	if err := validateArg("region", c.Region); err != nil {
		return nil, err
	}
	args := make([]string, 0, len(opArgs)+9)
	args = append(args, "ec2", subcommand)
	args = append(args, opArgs...)
	args = append(args,
		"--profile", c.Profile,
		"--region", c.Region,
		"--output", "json",
		"--no-cli-pager",
	)
	return args, nil
}

// ListTemplates returns all launch templates in the account/region.
func (c Client) ListTemplates() ([]Template, error) {
	args, err := c.command("describe-launch-templates", nil)
	if err != nil {
		return nil, err
	}
	out, err := c.runner()(args, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		LaunchTemplates []struct {
			LaunchTemplateId     string `json:"LaunchTemplateId"`
			LaunchTemplateName   string `json:"LaunchTemplateName"`
			CreateTime           string `json:"CreateTime"`
			CreatedBy            string `json:"CreatedBy"`
			DefaultVersionNumber int64  `json:"DefaultVersionNumber"`
			LatestVersionNumber  int64  `json:"LatestVersionNumber"`
		} `json:"LaunchTemplates"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse describe-launch-templates output: %w", err)
	}
	templates := make([]Template, 0, len(resp.LaunchTemplates))
	for _, t := range resp.LaunchTemplates {
		templates = append(templates, Template{
			ID:             t.LaunchTemplateId,
			Name:           t.LaunchTemplateName,
			DefaultVersion: t.DefaultVersionNumber,
			LatestVersion:  t.LatestVersionNumber,
			CreatedBy:      t.CreatedBy,
			CreateTime:     t.CreateTime,
		})
	}
	return templates, nil
}

// ListVersions returns every version of the named launch template.
func (c Client) ListVersions(name string) ([]Version, error) {
	if err := validateTemplateName(name); err != nil {
		return nil, err
	}
	args, err := c.command("describe-launch-template-versions", []string{"--launch-template-name", name})
	if err != nil {
		return nil, err
	}
	out, err := c.runner()(args, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		LaunchTemplateVersions []struct {
			VersionNumber      int64  `json:"VersionNumber"`
			VersionDescription string `json:"VersionDescription"`
			DefaultVersion     bool   `json:"DefaultVersion"`
			CreateTime         string `json:"CreateTime"`
		} `json:"LaunchTemplateVersions"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse describe-launch-template-versions output: %w", err)
	}
	versions := make([]Version, 0, len(resp.LaunchTemplateVersions))
	for _, v := range resp.LaunchTemplateVersions {
		versions = append(versions, Version{
			Number:      v.VersionNumber,
			Description: v.VersionDescription,
			IsDefault:   v.DefaultVersion,
			CreateTime:  v.CreateTime,
		})
	}
	return versions, nil
}

// GetVersionData returns the LaunchTemplateData for one version both as raw JSON
// (for creating the next version from) and flattened (for a key/value editor).
func (c Client) GetVersionData(name, version string) (json.RawMessage, map[string]string, error) {
	if err := validateTemplateName(name); err != nil {
		return nil, nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, nil, err
	}
	args, err := c.command("describe-launch-template-versions",
		[]string{"--launch-template-name", name, "--versions", version})
	if err != nil {
		return nil, nil, err
	}
	out, err := c.runner()(args, nil)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		LaunchTemplateVersions []struct {
			LaunchTemplateData json.RawMessage `json:"LaunchTemplateData"`
		} `json:"LaunchTemplateVersions"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, nil, fmt.Errorf("parse describe-launch-template-versions output: %w", err)
	}
	if len(resp.LaunchTemplateVersions) == 0 {
		return nil, nil, fmt.Errorf("launch template %q version %q not found", name, version)
	}
	raw := resp.LaunchTemplateVersions[0].LaunchTemplateData
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, nil, fmt.Errorf("parse launch template data: %w", err)
	}
	return raw, flatjson.Flatten(data), nil
}

// CreateVersion builds a new version of the template by applying edits to the
// data of sourceVersion. The resulting data is written to a 0600 temp file and
// passed with file:// — launch template data can embed user-data secrets, so it
// is never placed on the command line.
func (c Client) CreateVersion(name, sourceVersion string, edits map[string]string, description string) (int64, error) {
	if err := validateTemplateName(name); err != nil {
		return 0, err
	}
	if err := validateVersion(sourceVersion); err != nil {
		return 0, err
	}

	raw, _, err := c.GetVersionData(name, sourceVersion)
	if err != nil {
		return 0, err
	}
	var source any
	if err := json.Unmarshal(raw, &source); err != nil {
		return 0, fmt.Errorf("parse source launch template data: %w", err)
	}
	edited, err := flatjson.Unflatten(source, edits)
	if err != nil {
		return 0, fmt.Errorf("apply edits: %w", err)
	}
	body, err := json.Marshal(edited)
	if err != nil {
		return 0, fmt.Errorf("encode launch template data: %w", err)
	}

	tmp, err := os.CreateTemp("", "ezcloud-lt-*.json")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}

	args, err := c.command("create-launch-template-version", []string{
		"--launch-template-name", name,
		"--source-version", sourceVersion,
		"--version-description", description,
		"--launch-template-data", "file://" + tmpPath,
	})
	if err != nil {
		return 0, err
	}
	out, err := c.runner()(args, nil)
	if err != nil {
		return 0, err
	}
	var resp struct {
		LaunchTemplateVersion struct {
			VersionNumber int64 `json:"VersionNumber"`
		} `json:"LaunchTemplateVersion"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, fmt.Errorf("parse create-launch-template-version output: %w", err)
	}
	return resp.LaunchTemplateVersion.VersionNumber, nil
}

// SetDefaultVersion points the template's default at the given version.
func (c Client) SetDefaultVersion(name, version string) error {
	if err := validateTemplateName(name); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}
	args, err := c.command("modify-launch-template",
		[]string{"--launch-template-name", name, "--default-version", version})
	if err != nil {
		return err
	}
	_, err = c.runner()(args, nil)
	return err
}

// DeleteVersions deletes the listed versions. delete-launch-template-versions
// reports per-version failures in its body rather than failing the call, so the
// response is inspected and any unsuccessful deletions are surfaced as an error.
func (c Client) DeleteVersions(name string, versions []string) error {
	if err := validateTemplateName(name); err != nil {
		return err
	}
	if len(versions) == 0 {
		return errors.New("no versions specified")
	}
	for _, v := range versions {
		if err := validateVersion(v); err != nil {
			return err
		}
	}
	opArgs := append([]string{"--launch-template-name", name, "--versions"}, versions...)
	args, err := c.command("delete-launch-template-versions", opArgs)
	if err != nil {
		return err
	}
	out, err := c.runner()(args, nil)
	if err != nil {
		return err
	}
	var resp struct {
		UnsuccessfullyDeletedLaunchTemplateVersions []struct {
			VersionNumber int64 `json:"VersionNumber"`
			ResponseError struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"ResponseError"`
		} `json:"UnsuccessfullyDeletedLaunchTemplateVersions"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("parse delete-launch-template-versions output: %w", err)
	}
	if len(resp.UnsuccessfullyDeletedLaunchTemplateVersions) > 0 {
		parts := make([]string, 0, len(resp.UnsuccessfullyDeletedLaunchTemplateVersions))
		for _, u := range resp.UnsuccessfullyDeletedLaunchTemplateVersions {
			parts = append(parts, fmt.Sprintf("v%d: %s", u.VersionNumber, strings.TrimSpace(u.ResponseError.Message)))
		}
		return fmt.Errorf("failed to delete %d version(s): %s", len(parts), strings.Join(parts, "; "))
	}
	return nil
}

// validateArg rejects an empty or unsafe profile/region before it reaches argv.
func validateArg(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !safeArgRe.MatchString(value) {
		return fmt.Errorf("%s %q contains characters outside [A-Za-z0-9_.:/=+@-]", label, value)
	}
	return nil
}

// validateTemplateName requires a non-empty name free of whitespace and control
// characters. AWS launch template names are otherwise punctuation-tolerant, so
// this only guards against argv-corrupting input, not AWS's own naming rules.
func validateTemplateName(name string) error {
	if name == "" {
		return errors.New("launch template name is required")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("launch template name must not contain whitespace or control characters")
		}
	}
	return nil
}

// validateVersion accepts a numeric version or the AWS aliases $Latest/$Default.
func validateVersion(version string) error {
	if version == "$Latest" || version == "$Default" {
		return nil
	}
	if !versionRe.MatchString(version) {
		return fmt.Errorf("invalid version %q (want digits, $Latest, or $Default)", version)
	}
	return nil
}
