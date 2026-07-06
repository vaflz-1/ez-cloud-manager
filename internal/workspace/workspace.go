// Package workspace stores named workspaces that group together the
// (provider, profile) pairs a user works with as a unit — for example a
// "staging" workspace that references an AWS profile and a GCP profile.
//
// SECURITY: a workspace holds only references — provider and profile NAMES.
// It never stores credential material (access keys, tokens, secrets). Those
// live in each provider's own store (e.g. ~/.aws/credentials). Keeping this
// file secret-free means it can be backed up, synced, or shared without
// leaking anything sensitive; the perms are still tightened to 0600 as
// defense-in-depth.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// currentVersion is the on-disk schema version this build writes. List
	// refuses to read anything newer so an older binary cannot silently
	// misinterpret (and then overwrite) a file written by a newer one.
	currentVersion = 1
	// maxNameLen bounds a workspace name so a single entry cannot bloat the
	// file or a UI list; measured in Unicode code points, not bytes.
	maxNameLen = 64
)

// Member references one (provider, profile) pair by name. No secrets here.
type Member struct {
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
}

// Workspace is a named grouping of members.
type Workspace struct {
	Name    string   `json:"name"`
	Members []Member `json:"members"`
}

// file is the on-disk envelope. The explicit version lets future formats
// evolve without a reader from an older build mangling them.
type file struct {
	Version    int         `json:"version"`
	Workspaces []Workspace `json:"workspaces"`
}

// DefaultPath returns the workspaces.json location. EZCLOUD_CONFIG_DIR
// overrides the directory (useful for tests and non-standard setups);
// otherwise it falls back to ~/.config/ezcloud.
func DefaultPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspaces.json"), nil
}

func configDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("EZCLOUD_CONFIG_DIR")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ezcloud"), nil
}

// List returns all workspaces sorted by name. A missing or empty file is
// treated as "no workspaces yet", not an error. A file whose schema version
// is newer than this build is rejected rather than misread.
func List(path string) ([]Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Workspace{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []Workspace{}, nil
	}

	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version > currentVersion {
		return nil, fmt.Errorf("workspaces file %s was created by a newer version of ez-cloud-manager (schema version %d; this build understands up to %d)", path, f.Version, currentVersion)
	}
	if f.Workspaces == nil {
		return []Workspace{}, nil
	}
	sortByName(f.Workspaces)
	return f.Workspaces, nil
}

// Save upserts a workspace by name (case-sensitive) and rewrites the file
// atomically. The incoming workspace is validated and normalized first, so a
// bad entry never reaches disk.
func Save(path string, w Workspace) error {
	normalized, err := validateWorkspace(w)
	if err != nil {
		return err
	}
	list, err := List(path)
	if err != nil {
		return err
	}

	replaced := false
	for i := range list {
		if list[i].Name == normalized.Name {
			list[i] = normalized
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, normalized)
	}
	return write(path, list)
}

// Delete removes a workspace by name. Deleting an absent workspace is a
// no-op (and leaves the file untouched).
func Delete(path, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("workspace name is required")
	}
	list, err := List(path)
	if err != nil {
		return err
	}

	out := make([]Workspace, 0, len(list))
	removed := false
	for _, w := range list {
		if w.Name == name {
			removed = true
			continue
		}
		out = append(out, w)
	}
	if !removed {
		return nil
	}
	return write(path, out)
}

// Rename changes a workspace's name. The new name is validated like Save's,
// and the rename fails if a different workspace already uses it. Renaming a
// workspace to its own current name is allowed (a no-op that still rewrites).
func Rename(path, oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	if oldName == "" {
		return errors.New("current workspace name is required")
	}
	newName = strings.TrimSpace(newName)
	if err := validateName(newName); err != nil {
		return err
	}

	list, err := List(path)
	if err != nil {
		return err
	}

	srcIdx := -1
	for i := range list {
		if list[i].Name == oldName {
			srcIdx = i
			break
		}
	}
	if srcIdx < 0 {
		return fmt.Errorf("workspace %q not found", oldName)
	}
	for i := range list {
		if i != srcIdx && list[i].Name == newName {
			return fmt.Errorf("workspace %q already exists", newName)
		}
	}

	list[srcIdx].Name = newName
	return write(path, list)
}

// validateWorkspace returns a normalized copy (trimmed name, trimmed and
// deduplicated members) or an error if any field is invalid.
func validateWorkspace(w Workspace) (Workspace, error) {
	name := strings.TrimSpace(w.Name)
	if err := validateName(name); err != nil {
		return Workspace{}, err
	}
	members, err := normalizeMembers(w.Members)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{Name: name, Members: members}, nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("workspace name is required")
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return fmt.Errorf("workspace name must be at most %d characters", maxNameLen)
	}
	if hasControlChars(name) {
		return errors.New("workspace name must not contain control characters")
	}
	return nil
}

// normalizeMembers trims each member, rejects empty or control-laden fields,
// and drops duplicate (provider, profile) pairs while preserving order.
func normalizeMembers(in []Member) ([]Member, error) {
	out := make([]Member, 0, len(in))
	seen := make(map[Member]bool, len(in))
	for _, m := range in {
		provider := strings.TrimSpace(m.Provider)
		profile := strings.TrimSpace(m.Profile)
		if provider == "" {
			return nil, errors.New("workspace member provider must not be empty")
		}
		if profile == "" {
			return nil, errors.New("workspace member profile must not be empty")
		}
		if hasControlChars(provider) || hasControlChars(profile) {
			return nil, errors.New("workspace member must not contain control characters")
		}
		key := Member{Provider: provider, Profile: profile}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

// hasControlChars reports whether s contains any ASCII control character
// (including tab). Names and references are identifiers, so no control
// characters are legitimate here.
func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// write sorts the list, marshals the versioned envelope, and replaces the
// file atomically (temp file in the same dir, chmod 0600, rename). No backup
// files are kept: this metadata holds no secrets, and the atomic rename means
// a crash leaves either the old file or the new one, never a torn write.
func write(path string, list []Workspace) error {
	sortByName(list)
	payload := file{Version: currentVersion, Workspaces: list}
	if payload.Workspaces == nil {
		payload.Workspaces = []Workspace{}
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".workspaces.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func sortByName(list []Workspace) {
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
}
