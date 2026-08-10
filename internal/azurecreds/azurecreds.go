// Package azurecreds manages named Azure credential profiles.
//
// The Azure CLI has no native equivalent of AWS named profiles — one login
// context per machine is a long-standing pain point for people juggling
// tenants and clients. So this backend owns its storage: an INI file of
// profiles (default ~/.config/ezcloud/azure_profiles.ini, 0600) holding
// service-principal / subscription fields, exportable as the AZURE_* and
// ARM_* environment variables that the Azure CLI, SDKs and Terraform accept.
package azurecreds

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ez-cloud-manager/internal/inifile"
	"ez-cloud-manager/internal/pathlock"
)

const (
	KeyTenantID       = "tenant_id"
	KeyClientID       = "client_id"
	KeyClientSecret   = "client_secret"
	KeySubscriptionID = "subscription_id"
	KeyCloud          = "cloud"
	KeyLocation       = "location"
	KeyResourceGroup  = "resource_group"
)

// envToKey maps the env-var spellings accepted on import. Azure SDK/CLI use
// AZURE_*; Terraform's azurerm provider uses ARM_*.
var envToKey = map[string]string{
	"AZURE_TENANT_ID":         KeyTenantID,
	"AZURE_CLIENT_ID":         KeyClientID,
	"AZURE_CLIENT_SECRET":     KeyClientSecret,
	"AZURE_SUBSCRIPTION_ID":   KeySubscriptionID,
	"AZURE_CLOUD_NAME":        KeyCloud,
	"AZURE_LOCATION":          KeyLocation,
	"AZURE_DEFAULTS_LOCATION": KeyLocation,
	"AZURE_RESOURCE_GROUP":    KeyResourceGroup,
	"AZURE_DEFAULTS_GROUP":    KeyResourceGroup,
	"ARM_TENANT_ID":           KeyTenantID,
	"ARM_CLIENT_ID":           KeyClientID,
	"ARM_CLIENT_SECRET":       KeyClientSecret,
	"ARM_SUBSCRIPTION_ID":     KeySubscriptionID,
	"ARM_ENVIRONMENT":         KeyCloud,
}

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

// DefaultPath honors EZCLOUD_AZURE_PROFILES_FILE, then EZCLOUD_CONFIG_DIR,
// then ~/.config/ezcloud/azure_profiles.ini.
func DefaultPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("EZCLOUD_AZURE_PROFILES_FILE")); override != "" {
		return override, nil
	}
	if dir := strings.TrimSpace(os.Getenv("EZCLOUD_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "azure_profiles.ini"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ezcloud", "azure_profiles.ini"), nil
}

func List(path string) ([]ProfileSummary, error) {
	model, err := inifile.Read(path)
	if err != nil {
		return nil, err
	}
	profiles := make([]ProfileSummary, 0, len(model.Sections))
	for _, sec := range model.Sections {
		keys := make([]string, 0)
		for _, ln := range sec.Lines {
			if ln.Kind == inifile.LineKV {
				keys = append(keys, ln.Key)
			}
		}
		profiles = append(profiles, ProfileSummary{Name: sec.Name, Keys: keys})
	}
	return profiles, nil
}

func Get(path, name string) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Profile{}, errors.New("profile name is required")
	}
	model, err := inifile.Read(path)
	if err != nil {
		return Profile{}, err
	}
	idx := model.FindSection(name)
	if idx < 0 {
		return Profile{Name: name, Fields: map[string]string{}}, nil
	}
	return Profile{Name: model.Sections[idx].Name, Fields: model.Sections[idx].Fields()}, nil
}

func Save(path, name string, fields map[string]string) (resultErr error) {
	name = strings.TrimSpace(name)
	if err := inifile.ValidateSectionName(name); err != nil {
		return err
	}
	normalized := normalizeFields(fields)
	for key, value := range normalized {
		if err := inifile.ValidateField(key, value); err != nil {
			return err
		}
	}
	release, err := pathlock.Acquire(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
	}()

	model, err := inifile.Read(path)
	if err != nil {
		return err
	}
	idx := model.FindSection(name)
	if idx < 0 {
		model.Sections = append(model.Sections, inifile.Section{Name: name})
		idx = len(model.Sections) - 1
	}
	model.Sections[idx].ApplyFields(normalized)
	return inifile.WriteAtomic(path, model, true)
}

func Delete(path, name string) (resultErr error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name is required")
	}
	release, err := pathlock.Acquire(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
	}()
	model, err := inifile.Read(path)
	if err != nil {
		return err
	}
	if !model.DeleteSection(name) {
		return nil
	}
	return inifile.WriteAtomic(path, model, true)
}

// normalizeFields lowercases keys, maps env-var spellings to storage keys and
// trims whitespace, mirroring the AWS backend's behavior.
func normalizeFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.ToLower(strings.TrimSpace(key))
		key = strings.ReplaceAll(key, " ", "_")
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if mapped, ok := envToKey[strings.ToUpper(key)]; ok {
			key = mapped
		}
		out[key] = value
	}
	return out
}

// spJSONKeys maps `az ad sp create-for-rbac` output (and the camelCase
// spellings of SDK auth files) to storage keys. "password" is the client
// secret in create-for-rbac output.
var spJSONKeys = map[string]string{
	"appid":          KeyClientID,
	"clientid":       KeyClientID,
	"password":       KeyClientSecret,
	"clientsecret":   KeyClientSecret,
	"tenant":         KeyTenantID,
	"tenantid":       KeyTenantID,
	"subscriptionid": KeySubscriptionID,
}

var assignRe = regexp.MustCompile(`^\s*(?:export\s+|set\s+|\$env:)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`)
var sectionRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)

// Parse extracts Azure profile fields from pasted text: `az ad sp
// create-for-rbac` JSON, AZURE_*/ARM_* env lines (bash/cmd/PowerShell
// spellings) or an INI block.
func Parse(text string) Parsed {
	text = strings.TrimSpace(text)
	if parsed, ok := parseJSON(text); ok {
		return parsed
	}

	fields := map[string]string{}
	var profileName string
	scanner := strings.Split(text, "\n")
	for _, rawLine := range scanner {
		raw := strings.TrimSpace(rawLine)
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, ";") {
			continue
		}
		if match := sectionRe.FindStringSubmatch(raw); len(match) == 2 {
			if profileName == "" {
				profileName = strings.TrimSpace(match[1])
			}
			continue
		}
		if match := assignRe.FindStringSubmatch(raw); len(match) == 3 {
			key := strings.TrimSpace(match[1])
			value := cleanValue(match[2])
			if mapped, ok := envToKey[strings.ToUpper(key)]; ok {
				fields[mapped] = value
				continue
			}
			lower := strings.ToLower(key)
			if _, known := knownKeys[lower]; known {
				fields[lower] = value
			}
		}
	}
	return Parsed{ProfileName: profileName, Fields: fields}
}

var knownKeys = map[string]bool{
	KeyTenantID: true, KeyClientID: true, KeyClientSecret: true,
	KeySubscriptionID: true, KeyCloud: true, KeyLocation: true, KeyResourceGroup: true,
}

// parseJSON handles service-principal JSON blobs. Unknown JSON keys are
// deliberately dropped rather than stored: this file must only ever hold the
// fields the schema documents.
func parseJSON(text string) (Parsed, bool) {
	if !strings.HasPrefix(text, "{") {
		return Parsed{}, false
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(text), &blob); err != nil {
		return Parsed{}, false
	}
	fields := map[string]string{}
	var name string
	var notes []string
	for key, value := range blob {
		str, isString := value.(string)
		if !isString {
			continue
		}
		lower := strings.ToLower(key)
		if mapped, ok := spJSONKeys[lower]; ok {
			fields[mapped] = str
			continue
		}
		if lower == "displayname" {
			name = str
		}
	}
	if len(fields) == 0 {
		return Parsed{}, false
	}
	if _, hasSecret := fields[KeyClientSecret]; hasSecret {
		notes = append(notes, "Client secret captured from pasted JSON — it is masked in the UI and stored with 0600 permissions.")
	}
	return Parsed{ProfileName: name, Fields: fields, Notes: notes}, true
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
