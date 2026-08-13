// Package gcpcreds manages Google Cloud SDK named configurations — the
// native gcloud equivalent of AWS profiles.
//
// Storage is gcloud's own: one INI file per configuration at
// <config root>/configurations/config_<name>, with the active configuration
// name recorded in <config root>/active_config. Editing these files directly
// keeps full interop with the gcloud CLI (`gcloud config configurations …`).
//
// Field keys are dotted "section.property" (core.project, compute.region),
// mirroring gcloud's own property addressing.
package gcpcreds

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ez-cloud-manager/internal/inifile"
	"ez-cloud-manager/internal/pathlock"
)

// nowFunc is swappable in tests that assert backup naming.
var nowFunc = time.Now

const maxConfigBytes = 4 << 20

// writeBatchAtomic is replaceable only by package tests that prove rollback
// after a later row's filesystem failure. Production always points at the
// shared atomic writer.
var writeBatchAtomic = inifile.WriteAtomic

const (
	KeyAccount     = "core.account"
	KeyProject     = "core.project"
	KeyRegion      = "compute.region"
	KeyZone        = "compute.zone"
	KeyCredFile    = "auth.credential_file_override"
	KeyUsageReport = "core.disable_usage_reporting"
)

type ProfileSummary struct {
	Name string   `json:"name"`
	Keys []string `json:"keys"`
}

type Profile struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

type Parsed struct {
	ProfileName string            `json:"profileName,omitempty"`
	Fields      map[string]string `json:"fields"`
	Notes       []string          `json:"notes,omitempty"`
}

// ConditionalSave is one row in an optimistic batch transaction. Every row's
// precondition is validated while all destination path locks are held; a stale
// row aborts the batch before any configuration is written.
type ConditionalSave struct {
	Name           string
	Fields         map[string]string
	ExpectedFields map[string]string
	ExpectAbsent   bool
}

// ErrConflict is returned when a conditional save's editor baseline no longer
// matches the configuration currently on disk.
var ErrConflict = errors.New("gcloud configuration changed since it was loaded")

// DefaultPath returns the gcloud config root: $CLOUDSDK_CONFIG if set, else
// ~/.config/gcloud — the same resolution the gcloud CLI uses.
func DefaultPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("CLOUDSDK_CONFIG")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gcloud"), nil
}

func configFile(root, name string) (string, error) {
	name, err := validateConfigName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "configurations", "config_"+name), nil
}

func activeConfigFile(root string) string {
	return filepath.Join(root, "active_config")
}

// ActiveName returns the active configuration name ("" if none recorded).
func ActiveName(root string) string {
	data, err := os.ReadFile(activeConfigFile(root))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// List enumerates configurations under <root>/configurations. A missing
// directory means gcloud was never initialized — an empty list, not an error.
func List(root string) ([]ProfileSummary, error) {
	entries, err := os.ReadDir(filepath.Join(root, "configurations"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ProfileSummary{}, nil
		}
		return nil, err
	}
	profiles := make([]ProfileSummary, 0, len(entries))
	for _, entry := range entries {
		name, ok := strings.CutPrefix(entry.Name(), "config_")
		if !ok || entry.IsDir() || name == "" {
			continue
		}
		// Delete() leaves a "config_<name>.bak.<timestamp>" backup alongside
		// the real file (see backupStamp below) — cutting the "config_"
		// prefix alone would let that backup through as if it were a live
		// configuration named "<name>.bak.<timestamp>". nameRe (gcloud's own
		// naming rule, already trusted by Activate/Delete) rejects it since
		// the timestamp suffix contains dots — reusing it here is a single
		// guard against backups AND any other stray file in the directory,
		// not a special case for ".bak." specifically.
		if !nameRe.MatchString(name) {
			continue
		}
		fields, err := readConfig(root, name)
		if err != nil {
			continue // unreadable file: skip rather than fail the whole list
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		profiles = append(profiles, ProfileSummary{Name: name, Keys: keys})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

func Get(root, name string) (Profile, error) {
	var err error
	name, err = validateConfigName(name)
	if err != nil {
		return Profile{}, err
	}
	fields, err := readConfig(root, name)
	if err != nil {
		return Profile{}, err
	}
	return Profile{Name: name, Fields: fields}, nil
}

func readConfig(root, name string) (map[string]string, error) {
	path, err := configFile(root, name)
	if err != nil {
		return nil, err
	}
	model, err := inifile.ReadLimited(path, maxConfigBytes)
	if err != nil {
		return nil, err
	}
	return fieldsFromModel(model)
}

func fieldsFromModel(model inifile.Model) (map[string]string, error) {
	fields := map[string]string{}
	sections := map[string]bool{}
	for _, sec := range model.Sections {
		sectionName := strings.ToLower(strings.TrimSpace(sec.Name))
		if sections[sectionName] {
			return nil, fmt.Errorf("gcloud configuration contains duplicate section %q", sec.Name)
		}
		sections[sectionName] = true
		sectionFields, err := sec.StrictFields()
		if err != nil {
			return nil, err
		}
		for key, value := range sectionFields {
			canonical := sectionName + "." + key
			if _, duplicate := fields[canonical]; duplicate {
				return nil, fmt.Errorf("gcloud configuration contains duplicate property %q", canonical)
			}
			fields[canonical] = value
		}
	}
	return fields, nil
}

// nameRe is gcloud's configuration naming rule: lowercase letters, digits
// and hyphens, starting with a letter.
var nameRe = regexp.MustCompile(`^[a-z][-a-z0-9]*$`)

func validateConfigName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("configuration name %q must match %s (lowercase letters, digits, hyphens)", name, nameRe)
	}
	return name, nil
}

// Save writes the named configuration, preserving unknown properties and
// comments already in the file. Keys must be "section.property"; a bare key
// is treated as core.<key>.
func Save(root, name string, fields map[string]string) error {
	return save(root, name, fields, nil, false, false)
}

// SaveIfUnchanged compares and writes while holding the same configuration
// file lock, preventing stale editors and batch syncs from losing updates.
func SaveIfUnchanged(root, name string, fields, expectedFields map[string]string, expectAbsent bool) error {
	return save(root, name, fields, expectedFields, expectAbsent, true)
}

// SaveBatchIfUnchanged applies a set of configuration updates as one
// optimistic transaction with respect to conflicts: it locks every target in
// deterministic order and validates every baseline before the first write. If
// a later filesystem write fails, previously written rows are restored before
// the error returns; callers still refresh in case that rollback itself fails.
func SaveBatchIfUnchanged(root string, changes []ConditionalSave) (resultErr error) {
	if len(changes) == 0 {
		return nil
	}
	type preparedChange struct {
		name         string
		path         string
		bySection    map[string]map[string]string
		expected     map[string]string
		expectAbsent bool
		model        inifile.Model
		existed      bool
		original     []byte
	}
	prepared := make([]preparedChange, 0, len(changes))
	paths := make([]string, 0, len(changes))
	seenNames := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		name, err := validateConfigName(change.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seenNames[name]; duplicate {
			return fmt.Errorf("duplicate gcloud configuration %q in batch", name)
		}
		seenNames[name] = struct{}{}
		bySection, _, err := normalizeAndGroupFields(change.Fields)
		if err != nil {
			return err
		}
		_, expected, err := normalizeAndGroupFields(change.ExpectedFields)
		if err != nil {
			return fmt.Errorf("validate expected gcloud fields: %w", err)
		}
		path, err := configFile(root, name)
		if err != nil {
			return err
		}
		prepared = append(prepared, preparedChange{
			name: name, path: path, bySection: bySection,
			expected: expected, expectAbsent: change.ExpectAbsent,
		})
		paths = append(paths, path)
	}
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].path < prepared[j].path })

	release, err := pathlock.Acquire(paths...)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()

	// Preflight every row under the complete lock set before any write.
	for i := range prepared {
		model, err := inifile.ReadLimited(prepared[i].path, maxConfigBytes)
		if err != nil {
			return err
		}
		prepared[i].model = model
		currentFields, err := fieldsFromModel(model)
		if err != nil {
			return err
		}
		_, statErr := os.Stat(prepared[i].path)
		exists := statErr == nil
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if prepared[i].expectAbsent {
			if exists {
				return ErrConflict
			}
		} else if !exists || !maps.Equal(currentFields, prepared[i].expected) {
			return ErrConflict
		}
		prepared[i].existed = exists
		if exists {
			prepared[i].original = inifile.Render(model)
		}
	}

	written := make([]int, 0, len(prepared))
	for i := range prepared {
		applyFieldsToModel(&prepared[i].model, prepared[i].bySection)
		if err := writeBatchAtomic(prepared[i].path, prepared[i].model, true); err != nil {
			var rollbackErr error
			for j := len(written) - 1; j >= 0; j-- {
				previous := &prepared[written[j]]
				if previous.existed {
					rollbackErr = errors.Join(rollbackErr, inifile.WriteFileAtomic(previous.path, previous.original, false))
				} else if removeErr := os.Remove(previous.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					rollbackErr = errors.Join(rollbackErr, removeErr)
				}
			}
			if rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("rollback a partially written gcloud sync: %w", rollbackErr))
			}
			return err
		}
		written = append(written, i)
	}
	return nil
}

func save(root, name string, fields, expectedFields map[string]string, expectAbsent, conditional bool) (resultErr error) {
	var err error
	name, err = validateConfigName(name)
	if err != nil {
		return err
	}

	bySection, _, err := normalizeAndGroupFields(fields)
	if err != nil {
		return err
	}
	_, expected, err := normalizeAndGroupFields(expectedFields)
	if err != nil && conditional {
		return fmt.Errorf("validate expected gcloud fields: %w", err)
	}

	path, err := configFile(root, name)
	if err != nil {
		return err
	}
	release, err := pathlock.Acquire(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
	}()
	model, err := inifile.ReadLimited(path, maxConfigBytes)
	if err != nil {
		return err
	}
	currentFields, err := fieldsFromModel(model)
	if err != nil {
		return err
	}
	if conditional {
		_, statErr := os.Stat(path)
		exists := statErr == nil
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if expectAbsent {
			if exists {
				return ErrConflict
			}
		} else if !exists || !maps.Equal(currentFields, expected) {
			return ErrConflict
		}
	}

	applyFieldsToModel(&model, bySection)
	return inifile.WriteAtomic(path, model, true)
}

func applyFieldsToModel(model *inifile.Model, bySection map[string]map[string]string) {
	// Deterministic section order for new sections: core first, rest sorted.
	names := make([]string, 0, len(bySection))
	for sec := range bySection {
		names = append(names, sec)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "core" {
			return true
		}
		if names[j] == "core" {
			return false
		}
		return names[i] < names[j]
	})

	for _, sec := range names {
		idx := model.FindSection(sec)
		if idx < 0 {
			hasValue := false
			for _, v := range bySection[sec] {
				if v != "" {
					hasValue = true
					break
				}
			}
			if !hasValue {
				continue // don't create sections that would be empty
			}
			model.Sections = append(model.Sections, inifile.Section{Name: sec})
			idx = len(model.Sections) - 1
		}
		model.Sections[idx].ApplyFields(bySection[sec])
	}
}

func normalizeAndGroupFields(fields map[string]string) (map[string]map[string]string, map[string]string, error) {
	bySection := map[string]map[string]string{}
	normalized := map[string]string{}
	for key, value := range fields {
		sec, prop := splitKey(key)
		if sec == "" {
			return nil, nil, fmt.Errorf("invalid gcloud property key %q", key)
		}
		if err := inifile.ValidateField(sec, ""); err != nil {
			return nil, nil, fmt.Errorf("invalid gcloud property section %q: %w", sec, err)
		}
		value = strings.TrimSpace(value)
		if err := inifile.ValidateField(prop, value); err != nil {
			return nil, nil, fmt.Errorf("invalid gcloud property %q: %w", prop, err)
		}
		if bySection[sec] == nil {
			bySection[sec] = map[string]string{}
		}
		bySection[sec][prop] = value
		normalized[sec+"."+prop] = value
	}
	return bySection, normalized, nil
}

// splitKey turns "compute.region" into ("compute", "region") and a bare
// "project" into ("core", "project"). Empty keys yield ("", "").
func splitKey(key string) (string, string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", ""
	}
	sec, prop, found := strings.Cut(key, ".")
	if !found {
		return "core", key
	}
	if sec == "" || prop == "" {
		return "", ""
	}
	return sec, prop
}

// Delete removes the configuration file (after a timestamped backup).
// Deleting the active configuration is refused, matching gcloud's behavior
// — switch first, then delete.
func Delete(root, name string) (resultErr error) {
	var err error
	name, err = validateConfigName(name)
	if err != nil {
		return err
	}
	path, err := configFile(root, name)
	if err != nil {
		return err
	}
	release, err := pathlock.Acquire(activeConfigFile(root), path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
	}()
	if ActiveName(root) == name {
		return fmt.Errorf("%q is the active gcloud configuration — activate another configuration first", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	backup := path + ".bak." + backupStamp()
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return err
	}
	return os.Remove(path)
}

// Activate makes name the active gcloud configuration by writing
// <root>/active_config — exactly what `gcloud config configurations
// activate` does.
func Activate(root, name string) (resultErr error) {
	var err error
	name, err = validateConfigName(name)
	if err != nil {
		return err
	}
	path, err := configFile(root, name)
	if err != nil {
		return err
	}
	release, err := pathlock.Acquire(activeConfigFile(root), path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
	}()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("configuration %q does not exist", name)
	}
	return inifile.WriteFileAtomic(activeConfigFile(root), []byte(name), false)
}

// envAliases maps common GOOGLE_* env spellings to properties; systematic
// CLOUDSDK_<SECTION>_<PROPERTY> vars are handled generically in Parse.
var envAliases = map[string]string{
	"GOOGLE_CLOUD_PROJECT":           KeyProject,
	"GCLOUD_PROJECT":                 KeyProject,
	"GOOGLE_PROJECT":                 KeyProject,
	"GOOGLE_APPLICATION_CREDENTIALS": KeyCredFile,
	"GOOGLE_REGION":                  KeyRegion,
	"GOOGLE_ZONE":                    KeyZone,
}

var assignRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*=\s*(.*?)\s*$`)
var sectionRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
var cloudsdkRe = regexp.MustCompile(`^CLOUDSDK_([A-Z0-9]+)_([A-Z0-9_]+)$`)

// Parse extracts configuration properties from pasted text: `gcloud config
// list` output (INI-ish), CLOUDSDK_*/GOOGLE_* env lines, dotted properties,
// or a service account key JSON.
//
// Service account private keys are deliberately NOT captured: the key stays
// in its file and only project/account/key-path style fields are imported.
func Parse(text string) Parsed {
	text = strings.TrimSpace(text)
	if parsed, ok := parseServiceAccountJSON(text); ok {
		return parsed
	}

	fields := map[string]string{}
	var profileName string
	currentSection := ""
	for _, rawLine := range strings.Split(text, "\n") {
		raw := strings.TrimSpace(rawLine)
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, ";") {
			continue
		}
		if match := sectionRe.FindStringSubmatch(raw); len(match) == 2 {
			currentSection = strings.ToLower(strings.TrimSpace(match[1]))
			continue
		}
		match := assignRe.FindStringSubmatch(raw)
		if len(match) != 3 {
			continue
		}
		key := strings.TrimSpace(match[1])
		value := cleanValue(match[2])
		upper := strings.ToUpper(key)
		if upper == "CLOUDSDK_ACTIVE_CONFIG_NAME" {
			profileName = value
			continue
		}
		if mapped, ok := envAliases[upper]; ok {
			fields[mapped] = value
			continue
		}
		if m := cloudsdkRe.FindStringSubmatch(upper); len(m) == 3 {
			fields[strings.ToLower(m[1])+"."+strings.ToLower(m[2])] = value
			continue
		}
		if strings.Contains(key, ".") {
			sec, prop := splitKey(key)
			if sec != "" {
				fields[sec+"."+prop] = value
			}
			continue
		}
		if currentSection != "" {
			fields[currentSection+"."+strings.ToLower(key)] = value
		}
	}
	return Parsed{ProfileName: profileName, Fields: fields}
}

// parseServiceAccountJSON imports the safe identity fields of a service
// account key file and refuses to store the private key material itself.
func parseServiceAccountJSON(text string) (Parsed, bool) {
	if !strings.HasPrefix(text, "{") {
		return Parsed{}, false
	}
	var blob struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(text), &blob); err != nil || blob.Type != "service_account" {
		return Parsed{}, false
	}
	fields := map[string]string{}
	if blob.ProjectID != "" {
		fields[KeyProject] = blob.ProjectID
	}
	if blob.ClientEmail != "" {
		fields[KeyAccount] = blob.ClientEmail
	}
	notes := []string{}
	if blob.PrivateKey != "" {
		notes = append(notes, "private_key was NOT imported — keep the key file on disk and point auth.credential_file_override (GOOGLE_APPLICATION_CREDENTIALS) at its path.")
	}
	if len(fields) == 0 {
		return Parsed{}, false
	}
	return Parsed{Fields: fields, Notes: notes}, true
}

func cleanValue(value string) string {
	value = strings.TrimSpace(value)
	for _, quote := range []string{"\"", "'"} {
		if strings.HasPrefix(value, quote) && strings.HasSuffix(value, quote) && len(value) >= 2 {
			value = strings.TrimPrefix(strings.TrimSuffix(value, quote), quote)
		}
	}
	return value
}

// backupStamp matches the awscreds backup timestamp format.
func backupStamp() string {
	return nowFunc().Format("20060102-150405")
}
