package profile

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// validateProfile returns a normalized copy (trimmed name, normalized
// accounts/env vars) or an error if any field is invalid. It never copies
// ID/Version/timestamps — callers (Create/Save) set those explicitly so a
// caller can never smuggle a forged ID or version through validation.
func validateProfile(p Profile) (Profile, error) {
	name := strings.TrimSpace(p.Name)
	if err := validateName(name); err != nil {
		return Profile{}, err
	}
	accounts, err := normalizeAccounts(p.Accounts)
	if err != nil {
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
	return Profile{
		Name:            name,
		ShowAllAccounts: p.ShowAllAccounts,
		Accounts:        accounts,
		EnvVars:         envVars,
		EnabledPlugins:  enabledPlugins,
		Settings:        p.Settings,
		WindowState:     p.WindowState,
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
// order — same shape as internal/workspace's normalizeMembers.
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
	if strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_") {
		return true
	}
	switch k {
	case "PATH", "SHELL", "IFS", "ENV", "BASH_ENV",
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
