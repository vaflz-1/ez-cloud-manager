package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"ez-cloud-manager/internal/provider"
)

// checkCommand is `ezcloud check` — Test Connection: runs the provider's own
// vendor-CLI identity call for one profile (see provider.Checker) and prints
// the outcome as CheckResult JSON. A provider that doesn't implement Checker
// (currently: azure) is reported the same clean way as any other failed
// check — {"ok":false,"error":"…"} at exit 0 — never a CLI failure, so the UI
// can render it identically to a real connectivity failure.
func checkCommand(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	providerID := fs.String("provider", defaultProvider, "provider id (aws, gcp, azure)")
	profileName := fs.String("profile", "", "profile name")
	timeoutSec := fs.Int("timeout", 10, "seconds before the check is aborted")
	_ = fs.Parse(args)
	if *profileName == "" {
		fail(fmt.Errorf("--profile is required"))
	}

	prov, err := provider.Get(*providerID)
	if err != nil {
		fail(err)
	}
	path, err := prov.DefaultPath()
	if err != nil {
		fail(err)
	}

	checker, ok := prov.(provider.Checker)
	if !ok {
		writeJSON(provider.CheckResult{OK: false, Error: fmt.Sprintf("%s does not support Test Connection yet", prov.DisplayName())})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()
	result, err := checker.Check(ctx, path, *profileName)
	if err != nil {
		fail(err)
	}
	// Key names/profile only — identity facts and any vendor-CLI error text
	// stay out of the audit log, matching every other audit entry's
	// "never values" invariant.
	auditRecordKeys("check", prov.ID(), *profileName, nil)
	writeJSON(result)
}
