package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ez-cloud-manager/internal/connectionsync"
	"ez-cloud-manager/internal/provider"
)

func connectionsAuthCommand(args []string) {
	if len(args) == 0 {
		fail(fmt.Errorf("usage: ezcloud connections auth discover|login|apply --provider aws|gcp"))
	}
	subcommand, rest := args[0], args[1:]
	fs := flag.NewFlagSet("connections auth "+subcommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	providerID := fs.String("provider", defaultProvider, "provider id (aws or gcp)")
	principal := fs.String("principal", "", "locally credentialed GCP principal")
	if err := fs.Parse(rest); err != nil || len(fs.Args()) != 0 {
		fail(fmt.Errorf("invalid connections auth arguments"))
	}
	if *providerID != "aws" && *providerID != "gcp" {
		fail(fmt.Errorf("provider %q does not support sign-in sync", *providerID))
	}

	manager, err := newConnectionSyncManager()
	if err != nil {
		fail(err)
	}

	switch subcommand {
	case "discover":
		ctx, cancel := connectionAuthContext(90 * time.Second)
		defer cancel()
		snapshot, err := manager.Discover(ctx, *providerID, *principal)
		if err != nil {
			fail(connectionAuthError(err))
		}
		writeJSON(snapshot)
	case "login":
		if *principal != "" {
			fail(fmt.Errorf("--principal is not accepted by login"))
		}
		var request connectionsync.LoginRequest
		if err := decodeAuthRequest(&request); err != nil {
			fail(err)
		}
		ctx, cancel := connectionAuthContext(10 * time.Minute)
		defer cancel()
		response, err := manager.Login(ctx, *providerID, request)
		if err != nil {
			auditRecordKeys("auth-login-failed", *providerID, "", nil)
			fail(connectionAuthError(err))
		}
		auditRecordKeys("auth-login", *providerID, "", nil)
		writeJSON(response)
	case "apply":
		if *principal != "" {
			fail(fmt.Errorf("principal belongs in the apply JSON body"))
		}
		var request connectionsync.ApplyRequest
		if err := decodeAuthRequest(&request); err != nil {
			fail(err)
		}
		ctx, cancel := connectionAuthContext(2 * time.Minute)
		defer cancel()
		response, err := manager.ApplyGuarded(ctx, *providerID, request, func(providerID, storePath string, names []string) error {
			for _, name := range names {
				if err := removeConnectionRefsFromMatchingWorkspaces(providerID, name, storePath); err != nil {
					return fmt.Errorf("remove stale workspace grants for %q: %w", name, err)
				}
			}
			return nil
		})
		if err != nil {
			auditRecordKeys("auth-sync-failed", *providerID, "", nil)
			fail(connectionAuthError(err))
		}
		auditRecordKeys("auth-sync", *providerID, "", nil)
		writeJSON(response)
	default:
		fail(fmt.Errorf("unknown connections auth subcommand"))
	}
}

// connectionAuthContext connects app/terminal cancellation to vendor CLI
// process-group cancellation. Swift first sends SIGTERM to the core process;
// NotifyContext gives the manager time to kill the isolated vendor group and
// remove any scratch gcloud configuration before the UI's SIGKILL fallback.
func connectionAuthContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	timedContext, cancelTimeout := context.WithTimeout(signalContext, timeout)
	return timedContext, func() {
		cancelTimeout()
		stopSignals()
	}
}

func connectionAuthError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("connection sign-in operation canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("connection sign-in operation timed out")
	default:
		return err
	}
}

func newConnectionSyncManager() (*connectionsync.Manager, error) {
	awsConfigPath, err := connectionsync.DefaultAWSConfigPath()
	if err != nil {
		return nil, err
	}
	gcpConfigRoot, err := connectionsync.DefaultGCPConfigRoot()
	if err != nil {
		return nil, err
	}
	gcpBackend, err := provider.Get("gcp")
	if err != nil {
		return nil, err
	}
	awsBackend, err := provider.Get("aws")
	if err != nil {
		return nil, err
	}
	awsCredentialsPath, err := awsBackend.DefaultPath()
	if err != nil {
		return nil, err
	}
	return connectionsync.New(connectionsync.ExecRunner{}, awsConfigPath, awsCredentialsPath, gcpConfigRoot, gcpBackend)
}

func decodeAuthRequest(destination any) error {
	if err := decodeLimitedJSON(os.Stdin, destination); err != nil {
		return fmt.Errorf("read connections auth JSON: %w", err)
	}
	return nil
}
