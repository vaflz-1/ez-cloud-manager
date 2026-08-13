package connectionsync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"ez-cloud-manager/internal/gcpcreds"
	"ez-cloud-manager/internal/provider"
)

const (
	maxAWSSSOProfiles = 100
	maxGCPProjects    = 1000
)

type Manager struct {
	runner             Runner
	awsConfigPath      string
	awsCredentialsPath string
	gcpConfigRoot      string
	gcpStore           provider.Provider
	nonce              func() (string, error)
	removeAll          func(string) error
}

func New(runner Runner, awsConfigPath, awsCredentialsPath, gcpConfigRoot string, gcpStore provider.Provider) (*Manager, error) {
	if runner == nil {
		return nil, fmt.Errorf("connection sync runner is required")
	}
	if strings.TrimSpace(awsConfigPath) == "" {
		return nil, fmt.Errorf("AWS config path is required")
	}
	if strings.TrimSpace(awsCredentialsPath) == "" {
		return nil, fmt.Errorf("AWS credentials path is required")
	}
	if strings.TrimSpace(gcpConfigRoot) == "" {
		return nil, fmt.Errorf("gcloud config root is required")
	}
	if gcpStore == nil || gcpStore.ID() != "gcp" {
		return nil, fmt.Errorf("GCP provider backend is required")
	}
	return &Manager{
		runner:             runner,
		awsConfigPath:      awsConfigPath,
		awsCredentialsPath: awsCredentialsPath,
		gcpConfigRoot:      gcpConfigRoot,
		gcpStore:           gcpStore,
		nonce:              randomNonce,
		removeAll:          os.RemoveAll,
	}, nil
}

func DefaultAWSConfigPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AWS_CONFIG_FILE")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "config"), nil
}

func DefaultGCPConfigRoot() (string, error) { return gcpcreds.DefaultPath() }

func (m *Manager) Discover(ctx context.Context, providerID, principal string) (DiscoverySnapshot, error) {
	switch providerID {
	case "aws":
		if strings.TrimSpace(principal) != "" {
			return DiscoverySnapshot{}, fmt.Errorf("AWS discovery does not accept a principal")
		}
		snapshot, _, err := m.discoverAWS()
		return snapshot, err
	case "gcp":
		snapshot, _, err := m.discoverGCP(ctx, strings.TrimSpace(principal), "")
		return snapshot, err
	default:
		return DiscoverySnapshot{}, fmt.Errorf("provider %q does not support sign-in sync", providerID)
	}
}

func (m *Manager) Login(ctx context.Context, providerID string, request LoginRequest) (LoginResponse, error) {
	switch providerID {
	case "aws":
		return m.loginAWS(ctx, request)
	case "gcp":
		return m.loginGCP(ctx)
	default:
		return LoginResponse{}, fmt.Errorf("provider %q does not support sign-in sync", providerID)
	}
}

// CreateGuard runs after a fresh discovery snapshot and all request
// preconditions have been validated, but before a provider creates a new
// Connection record. It lets the platform remove stale Workspace grants for a
// previously deleted name without briefly exposing the new record through
// those old permissions. Material identity replacement under an existing name
// is rejected instead because it cannot share this cross-store transaction.
//
// A guard must be idempotent. It may run before a later provider CAS failure;
// removing a stale grant is safe in that case, while restoring one is not.
type CreateGuard func(providerID, storePath string, names []string) error

func (m *Manager) Apply(ctx context.Context, providerID string, request ApplyRequest) (ApplyResponse, error) {
	return m.ApplyGuarded(ctx, providerID, request, nil)
}

// ApplyGuarded is the platform-integrated variant of Apply. Package callers
// that do not own Workspace authorization state can continue to use Apply.
func (m *Manager) ApplyGuarded(ctx context.Context, providerID string, request ApplyRequest, guard CreateGuard) (ApplyResponse, error) {
	switch providerID {
	case "aws":
		return m.applyAWS(request)
	case "gcp":
		return m.applyGCP(ctx, request, guard)
	default:
		return ApplyResponse{}, fmt.Errorf("provider %q does not support sign-in sync", providerID)
	}
}

func randomNonce() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func stableID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func revision(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func safeIdentifier(value string, max int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		// Category C also rejects bidi/zero-width format controls, surrogate
		// code points, and private-use/unassigned runes that make identity names
		// visually deceptive in selection UI.
		if unicode.Is(unicode.C, r) {
			return false
		}
	}
	return true
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
