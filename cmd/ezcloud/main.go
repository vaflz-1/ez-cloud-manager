// Command ezcloud is the JSON-speaking core behind the EZ Cloud Manager UI
// and a standalone CLI. Every command addresses one provider backend
// (--provider aws|gcp|azure, default aws so existing scripts and the AWS-only
// UI contract keep working unchanged).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"ez-cloud-manager/internal/export"
	"ez-cloud-manager/internal/provider"
	_ "ez-cloud-manager/internal/provider/awsprovider"   // register "aws"
	_ "ez-cloud-manager/internal/provider/azureprovider" // register "azure"
	_ "ez-cloud-manager/internal/provider/gcpprovider"   // register "gcp"
)

// defaultProvider preserves the pre-multi-cloud CLI/UI contract.
const defaultProvider = "aws"

type listResponse struct {
	Provider string                    `json:"provider"`
	Path     string                    `json:"path"`
	Profiles []provider.ProfileSummary `json:"profiles"`
}

type saveRequest struct {
	Fields map[string]string `json:"fields"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

type providerInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	// CanActivate tells the UI whether to offer a "make active/default"
	// action for this provider's profiles.
	CanActivate   bool   `json:"canActivate"`
	ActivateLabel string `json:"activateLabel,omitempty"`
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
	case "providers":
		infos := make([]providerInfo, 0)
		for _, id := range provider.IDs() {
			p, err := provider.Get(id)
			if err != nil {
				fail(err)
			}
			info := providerInfo{ID: id, DisplayName: p.DisplayName()}
			if act, ok := p.(provider.Activator); ok {
				info.CanActivate = true
				info.ActivateLabel = act.ActivateLabel()
			}
			infos = append(infos, info)
		}
		writeJSON(infos)
	case "list", "get", "save", "delete", "parse", "schema", "export", "activate":
		profileCommand(cmd, rest)
	case "ws":
		wsCommand(rest)
	case "audit":
		auditCommand(rest)
	case "lt":
		ltCommand(rest)
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
	format := fs.String("format", "env", "export format: env, dotenv, ini, json")
	_ = fs.Parse(args)

	prov, err := provider.Get(*providerID)
	if err != nil {
		fail(err)
	}
	path, err := prov.DefaultPath()
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
		profiles, err := prov.List(path)
		if err != nil {
			fail(err)
		}
		writeJSON(listResponse{Provider: prov.ID(), Path: path, Profiles: profiles})
	case "get":
		p, err := prov.Get(path, requireProfile())
		if err != nil {
			fail(err)
		}
		writeJSON(p)
	case "save":
		name := requireProfile()
		var req saveRequest
		if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&req); err != nil {
			fail(fmt.Errorf("read save json: %w", err))
		}
		old, _ := prov.Get(path, name)
		if err := prov.Save(path, name, req.Fields); err != nil {
			fail(err)
		}
		auditRecord("save", prov.ID(), name, old.Fields, req.Fields)
		writeJSON(okResponse{OK: true})
	case "delete":
		name := requireProfile()
		old, _ := prov.Get(path, name)
		if err := prov.Delete(path, name); err != nil {
			fail(err)
		}
		auditRecord("delete", prov.ID(), name, old.Fields, nil)
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
		p, err := prov.Get(path, name)
		if err != nil {
			fail(err)
		}
		out, err := export.Render(prov.Schema(), p.Name, p.Fields, *format)
		if err != nil {
			fail(err)
		}
		// Raw text on stdout (not JSON): exports are meant to be piped to
		// files/clipboard as-is.
		fmt.Print(out)
	case "activate":
		name := requireProfile()
		act, ok := prov.(provider.Activator)
		if !ok {
			fail(fmt.Errorf("provider %q has no activate action", prov.ID()))
		}
		if err := act.Activate(path, name); err != nil {
			fail(err)
		}
		auditRecord("activate", prov.ID(), name, nil, nil)
		writeJSON(okResponse{OK: true})
	}
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
  ezcloud providers
  ezcloud list     [--provider ID]
  ezcloud get      [--provider ID] --profile NAME
  ezcloud save     [--provider ID] --profile NAME < save.json
  ezcloud delete   [--provider ID] --profile NAME
  ezcloud parse    [--provider ID] < pasted-credentials.txt
  ezcloud schema   [--provider ID]
  ezcloud export   [--provider ID] --profile NAME [--format env|dotenv|ini|json]
  ezcloud activate [--provider ID] --profile NAME
  ezcloud ws       list | save | delete --name NAME | rename --old A --new B
  ezcloud audit    [--limit N]
  ezcloud lt       templates|versions|get|apply|set-default|delete-versions …

The default provider is "aws", preserving the original single-cloud behavior.`)
}
