package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"ez-cloud-manager/internal/provider"
	_ "ez-cloud-manager/internal/provider/awsprovider" // register the "aws" provider
)

// defaultProvider is used until the CLI/UI expose multi-provider selection.
// The AWS backend keeps the exact behavior and JSON shapes the UI depends on.
const defaultProvider = "aws"

type listResponse struct {
	Path     string                    `json:"path"`
	Profiles []provider.ProfileSummary `json:"profiles"`
}

type saveRequest struct {
	Fields map[string]string `json:"fields"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

// maxStdinBytes bounds how much we read from stdin (save JSON / parse blob).
// A credentials file is tiny; this just caps memory against a huge/hostile pipe.
const maxStdinBytes = 4 << 20 // 4 MiB

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	prov, err := provider.Get(defaultProvider)
	if err != nil {
		fail(err)
	}

	path, err := prov.DefaultPath()
	if err != nil {
		fail(err)
	}

	switch os.Args[1] {
	case "list":
		profiles, err := prov.List(path)
		if err != nil {
			fail(err)
		}
		writeJSON(listResponse{Path: path, Profiles: profiles})
	case "get":
		name := profileFlag(os.Args[2:])
		profile, err := prov.Get(path, name)
		if err != nil {
			fail(err)
		}
		writeJSON(profile)
	case "save":
		name := profileFlag(os.Args[2:])
		var req saveRequest
		if err := json.NewDecoder(io.LimitReader(os.Stdin, maxStdinBytes)).Decode(&req); err != nil {
			fail(fmt.Errorf("read save json: %w", err))
		}
		if err := prov.Save(path, name, req.Fields); err != nil {
			fail(err)
		}
		writeJSON(okResponse{OK: true})
	case "delete":
		name := profileFlag(os.Args[2:])
		if err := prov.Delete(path, name); err != nil {
			fail(err)
		}
		writeJSON(okResponse{OK: true})
	case "parse":
		data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
		if err != nil {
			fail(err)
		}
		writeJSON(prov.Parse(string(data)))
	default:
		usage()
		os.Exit(2)
	}
}

func profileFlag(args []string) string {
	fs := flag.NewFlagSet("profile", flag.ExitOnError)
	profile := fs.String("profile", "", "AWS profile name")
	_ = fs.Parse(args)
	if *profile == "" {
		fail(fmt.Errorf("--profile is required"))
	}
	return *profile
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
  ezcloud list
  ezcloud get --profile NAME
  ezcloud save --profile NAME < save.json
  ezcloud delete --profile NAME
  ezcloud parse < pasted-credentials.txt`)
}
