package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"ez-cloud-manager/internal/plugin"
)

const (
	maxEnvKeyBytes   = 128
	maxEnvValueBytes = 16 << 10 // 16 KiB, enough for paths/context but not blobs
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateProfile returns a normalized copy (trimmed name, normalized env
// vars/settings) or an error if any field is invalid. It never copies
// ID/Version/timestamps — callers (Create/mutateProfile) set those explicitly
// so a caller can never smuggle a forged ID, version or save time through
// validation.
func validateProfile(p Profile) (Profile, error) {
	name := strings.TrimSpace(p.Name)
	if err := validateName(name); err != nil {
		return Profile{}, err
	}
	envVars, err := normalizeEnvVars(p.EnvVars)
	if err != nil {
		return Profile{}, err
	}
	enabledPlugins, err := normalizeEnabledPlugins(p.EnabledPlugins)
	if err != nil {
		return Profile{}, err
	}
	settings, err := normalizeSettings(p.Settings)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		Name:           name,
		EnvVars:        envVars,
		EnabledPlugins: enabledPlugins,
		Settings:       settings,
		WindowState:    p.WindowState,
	}, nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("profile name is required")
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return fmt.Errorf("profile name must be at most %d characters", maxNameLen)
	}
	if hasControlChars(name) {
		return errors.New("profile name must not contain control characters")
	}
	return nil
}

// normalizeAccounts trims each reference, rejects empty or control-laden
// fields, and drops duplicate (provider, account) pairs while preserving
// order — same shape as internal/workspace's normalizeMembers. Since P1.5
// this is called from normalizeCloudAccountsBlob rather than directly from
// validateProfile (Accounts moved off the core Profile), but the guarantee
// is unchanged.
func normalizeAccounts(in []AccountRef) ([]AccountRef, error) {
	out := make([]AccountRef, 0, len(in))
	seen := make(map[AccountRef]bool, len(in))
	for _, a := range in {
		provider := strings.TrimSpace(a.Provider)
		account := strings.TrimSpace(a.Account)
		if provider == "" {
			return nil, errors.New("account reference provider must not be empty")
		}
		if account == "" {
			return nil, errors.New("account reference account must not be empty")
		}
		if hasControlChars(provider) || hasControlChars(account) {
			return nil, errors.New("account reference must not contain control characters")
		}
		key := AccountRef{Provider: provider, Account: account}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

// normalizeEnvVars trims key/value, rejects empty or control-laden keys,
// hard-rejects keys that look like secrets (see looksLikeSecret — profiles
// hold references only, never secret material), and dedupes by key (last
// one wins, first-seen order preserved).
func normalizeEnvVars(in []EnvVar) ([]EnvVar, error) {
	if len(in) > maxEnvVars {
		return nil, fmt.Errorf("at most %d environment variables are allowed per profile", maxEnvVars)
	}

	order := make([]string, 0, len(in))
	byKey := make(map[string]string, len(in))
	for _, e := range in {
		key := strings.TrimSpace(e.Key)
		value := strings.TrimSpace(e.Value)
		if key == "" {
			return nil, errors.New("environment variable key must not be empty")
		}
		if len(key) > maxEnvKeyBytes || !envKeyRe.MatchString(key) {
			return nil, fmt.Errorf("environment variable key %q must be a POSIX name of at most %d bytes", key, maxEnvKeyBytes)
		}
		if len(value) > maxEnvValueBytes {
			return nil, fmt.Errorf("environment variable %q exceeds %d bytes", key, maxEnvValueBytes)
		}
		if hasControlChars(key) || hasControlChars(value) {
			return nil, errors.New("environment variable must not contain control characters")
		}
		if looksLikeSecret(key) {
			return nil, fmt.Errorf("environment variable %q looks like a secret — profiles store references only, never secret material", key)
		}
		if looksLikeHijackVar(key) {
			return nil, fmt.Errorf("environment variable %q can hijack subprocess execution and is not allowed in profiles", key)
		}
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = value
	}

	out := make([]EnvVar, 0, len(order))
	for _, key := range order {
		out = append(out, EnvVar{Key: key, Value: byKey[key]})
	}
	return out, nil
}

// normalizeEnabledPlugins trims/dedupes plugin ids, capped like
// normalizeEnvVars. It deliberately does NOT reject an id this build doesn't
// recognize (no check against plugin.ByID) — internal/profile stays
// registry-agnostic, the same "skip/keep what you don't recognize, never
// destroy it" philosophy List already uses for unreadable profiles. That is
// what lets a `profile import` of a foreign `.ezprofile` (or a future P2
// marketplace install) round-trip an id this build doesn't statically know,
// instead of silently stripping it on the next save. Consumers (the CLI's
// `plugins list`, the Swift hub) are the ones that filter to known ids for
// DISPLAY.
func normalizeEnabledPlugins(in []string) ([]string, error) {
	if len(in) > maxEnabledPlugins {
		return nil, fmt.Errorf("at most %d enabled plugins are allowed per profile", maxEnabledPlugins)
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if hasControlChars(id) {
			return nil, errors.New("enabled plugin id must not contain control characters")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// normalizeSettings validates the per-plugin settings map: caps the number
// of blobs and each blob's size, requires each key be a valid non-empty
// non-control-character id and each value be well-formed JSON, and — for the
// one key this package knows the shape of (plugin.CloudAccountsID) —
// delegates to normalizeCloudAccountsBlob so that key's contents get the
// same validation Accounts/ShowAllAccounts always had before P1.5. Every
// other key's blob is accepted as opaque, unvalidated JSON.
func normalizeSettings(in map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(in) > maxSettingsBlobs {
		return nil, fmt.Errorf("at most %d plugin settings blobs are allowed per profile", maxSettingsBlobs)
	}
	out := make(map[string]json.RawMessage, len(in))
	for id, raw := range in {
		id = strings.TrimSpace(id)
		if id == "" || hasControlChars(id) {
			return nil, errors.New("plugin settings key must be a valid non-empty id")
		}
		if len(raw) > maxSettingsBlobBytes {
			return nil, fmt.Errorf("plugin settings blob %q exceeds %d bytes", id, maxSettingsBlobBytes)
		}
		if !json.Valid(raw) {
			return nil, fmt.Errorf("plugin settings blob %q is not valid JSON", id)
		}
		if id == plugin.CloudAccountsID {
			normalized, err := normalizeCloudAccountsBlob(raw)
			if err != nil {
				return nil, err
			}
			raw = normalized
		}
		out[id] = raw
	}
	return out, nil
}

// normalizeCloudAccountsBlob re-validates a Settings[plugin.CloudAccountsID]
// blob's Accounts the same way the pre-P1.5 top-level Profile.Accounts field
// always was (reusing normalizeAccounts unchanged — same control-char/dedupe
// guarantee it always had) and returns the re-marshaled, normalized blob.
func normalizeCloudAccountsBlob(raw json.RawMessage) (json.RawMessage, error) {
	var s CloudAccountsSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("cloud-accounts settings: %w", err)
	}
	accounts, err := normalizeAccounts(s.Accounts)
	if err != nil {
		return nil, err
	}
	return json.Marshal(CloudAccountsSettings{ShowAllAccounts: s.ShowAllAccounts, Accounts: accounts})
}

// looksLikeSecret mirrors the Swift UI's isSecretKey substring fallback, but
// here it is a hard Save/Create-time rejection rather than a display hint —
// PLATFORM.md requires profiles to never hold secrets.
func looksLikeSecret(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(k, "secret") || strings.Contains(k, "token") ||
		strings.Contains(k, "password") || strings.Contains(k, "private_key")
}

// looksLikeHijackVar reports whether key is a known process-hijack variable:
// a dynamic linker / interpreter-injection knob (LD_*, DYLD_*, NODE_OPTIONS,
// PYTHONSTARTUP, …) or PATH/SHELL/IFS itself. A malicious imported profile
// could otherwise set one of these and achieve code execution the moment its
// env vars are applied to a vendor CLI subprocess (see
// CredentialsService.childEnvironment on the Swift side) — this is a hard
// rejection, just like looksLikeSecret, not a UI-only warning.
func looksLikeHijackVar(key string) bool {
	k := strings.ToUpper(strings.TrimSpace(key))
	if strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_") ||
		strings.HasPrefix(k, "EZCLOUD_") || strings.HasPrefix(k, "KERVIK_") {
		return true
	}
	switch k {
	case "PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "SHELL", "IFS", "ENV", "BASH_ENV",
		"PYTHONPATH", "PYTHONSTARTUP", "PYTHONHOME",
		"NODE_OPTIONS", "PERL5OPT", "RUBYOPT",
		"GIT_SSH_COMMAND", "GIT_ASKPASS", "SSH_ASKPASS", "GCONV_PATH":
		return true
	default:
		return false
	}
}

// hasControlChars reports whether s contains any ASCII control character
// (including tab). Names, references and env var keys are identifiers, so no
// control characters are legitimate here.
func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// nameTaken reports whether name (case-insensitive) is used by a profile in
// list other than excludeID (pass "" to check against every profile).
func nameTaken(list []Profile, name, excludeID string) bool {
	for _, p := range list {
		if p.ID == excludeID && excludeID != "" {
			continue
		}
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

// uniqueName returns name if it is free, else appends " (2)", " (3)", …
// until one is.
func uniqueName(list []Profile, name, excludeID string) string {
	if !nameTaken(list, name, excludeID) {
		return name
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)", name, n)
		if !nameTaken(list, candidate, excludeID) {
			return candidate
		}
	}
}

func sortByName(list []Profile) {
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}
