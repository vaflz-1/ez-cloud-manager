package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ez-cloud-manager/internal/awslt"
)

// ltCommand exposes EC2 Launch Template operations. The app's editing model
// is deliberately clone-edit-apply: a new version is created from a source
// version and (optionally) made default; the source is never mutated, so the
// previous state always exists to roll back to. Deleting versions is its own
// explicit subcommand — never a side effect.
func ltCommand(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: ezcloud lt templates|versions|get|apply|set-default|delete-versions …"))
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("lt "+sub, flag.ExitOnError)
	profile := fs.String("profile", "", "AWS profile name")
	region := fs.String("region", "", "AWS region")
	name := fs.String("name", "", "launch template name")
	version := fs.String("version", "", "template version number")
	versions := fs.String("versions", "", "comma-separated version numbers")
	_ = fs.Parse(rest)

	if *profile == "" || *region == "" {
		fail(fmt.Errorf("--profile and --region are required"))
	}
	client := awslt.Client{Profile: *profile, Region: *region}

	requireName := func() string {
		if *name == "" {
			fail(fmt.Errorf("--name is required"))
		}
		return *name
	}

	switch sub {
	case "templates":
		templates, err := client.ListTemplates()
		if err != nil {
			fail(err)
		}
		out := make([]ltTemplateJSON, len(templates))
		for i, t := range templates {
			out[i] = ltTemplateJSON{t.ID, t.Name, t.DefaultVersion, t.LatestVersion, t.CreatedBy, t.CreateTime}
		}
		writeJSON(out)
	case "versions":
		list, err := client.ListVersions(requireName())
		if err != nil {
			fail(err)
		}
		out := make([]ltVersionJSON, len(list))
		for i, v := range list {
			out[i] = ltVersionJSON{v.Number, v.Description, v.IsDefault, v.CreateTime}
		}
		writeJSON(out)
	case "get":
		if *version == "" {
			fail(fmt.Errorf("--version is required"))
		}
		_, flat, err := client.GetVersionData(requireName(), *version)
		if err != nil {
			fail(err)
		}
		writeJSON(struct {
			Fields map[string]string `json:"fields"`
		}{Fields: flat})
	case "apply":
		templateName := requireName()
		var req struct {
			SourceVersion string            `json:"sourceVersion"`
			Description   string            `json:"description"`
			SetDefault    bool              `json:"setDefault"`
			Fields        map[string]string `json:"fields"`
		}
		if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&req); err != nil {
			fail(fmt.Errorf("read apply json: %w", err))
		}
		if len(req.Fields) == 0 {
			fail(fmt.Errorf("no edits to apply"))
		}
		newVersion, err := client.CreateVersion(templateName, req.SourceVersion, req.Fields, req.Description)
		if err != nil {
			fail(err)
		}
		if req.SetDefault {
			if err := client.SetDefaultVersion(templateName, fmt.Sprint(newVersion)); err != nil {
				// The version WAS created; report that alongside the failure
				// so the user does not re-apply and duplicate it.
				fail(fmt.Errorf("created version %d, but setting it default failed: %w", newVersion, err))
			}
		}
		editedKeys := make([]string, 0, len(req.Fields))
		for key := range req.Fields {
			editedKeys = append(editedKeys, key)
		}
		auditRecordKeys("lt-apply", "aws",
			fmt.Sprintf("%s@%s %s (v%s → v%d)", *profile, *region, templateName, req.SourceVersion, newVersion),
			editedKeys)
		writeJSON(struct {
			NewVersion int64 `json:"newVersion"`
			SetDefault bool  `json:"setDefault"`
		}{NewVersion: newVersion, SetDefault: req.SetDefault})
	case "set-default":
		if *version == "" {
			fail(fmt.Errorf("--version is required"))
		}
		if err := client.SetDefaultVersion(requireName(), *version); err != nil {
			fail(err)
		}
		auditRecord("lt-set-default", "aws", *profile, nil, nil)
		writeJSON(okResponse{OK: true})
	case "delete-versions":
		if *versions == "" {
			fail(fmt.Errorf("--versions is required (comma-separated)"))
		}
		list := strings.Split(*versions, ",")
		for i := range list {
			list[i] = strings.TrimSpace(list[i])
		}
		if err := client.DeleteVersions(requireName(), list); err != nil {
			fail(err)
		}
		auditRecord("lt-delete-versions", "aws", *profile, nil, nil)
		writeJSON(okResponse{OK: true})
	default:
		fail(fmt.Errorf("unknown lt subcommand %q", sub))
	}
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

