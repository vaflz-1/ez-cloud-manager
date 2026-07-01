package awscreds

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	KeyAccessKeyID     = "aws_access_key_id"
	KeySecretAccessKey = "aws_secret_access_key"
	KeySessionToken    = "aws_session_token"
	KeyRegion          = "region"
	KeyOutput          = "output"
	KeyCABundle        = "ca_bundle"
	KeyRoleArn         = "role_arn"
	KeySourceProfile   = "source_profile"
	KeyMfaSerial       = "mfa_serial"
	KeyDurationSeconds = "duration_seconds"
	KeyCredentialProc  = "credential_process"
	KeyCredentialSrc   = "credential_source"
	KeyRoleSessionName = "role_session_name"
	KeyExternalID      = "external_id"
	KeyWebIdentityFile = "web_identity_token_file"
	KeyEndpointURL     = "endpoint_url"
	KeyCLIPager        = "cli_pager"
	KeyRetryMode       = "retry_mode"
	KeyMaxAttempts     = "max_attempts"
	KeySTSEndpoints    = "sts_regional_endpoints"
	KeySSOSession      = "sso_session"
	KeySSOStartURL     = "sso_start_url"
	KeySSORegion       = "sso_region"
	KeySSOAccountID    = "sso_account_id"
	KeySSORoleName     = "sso_role_name"
)

var envToCredentialKey = map[string]string{
	"AWS_ACCESS_KEY_ID":           KeyAccessKeyID,
	"AWS_SECRET_ACCESS_KEY":       KeySecretAccessKey,
	"AWS_SESSION_TOKEN":           KeySessionToken,
	"AWS_DEFAULT_REGION":          KeyRegion,
	"AWS_REGION":                  KeyRegion,
	"AWS_DEFAULT_OUTPUT":          KeyOutput,
	"AWS_OUTPUT":                  KeyOutput,
	"AWS_CA_BUNDLE":               KeyCABundle,
	"AWS_ROLE_ARN":                KeyRoleArn,
	"AWS_SOURCE_PROFILE":          KeySourceProfile,
	"AWS_MFA_SERIAL":              KeyMfaSerial,
	"AWS_DURATION_SECONDS":        KeyDurationSeconds,
	"AWS_CREDENTIAL_SOURCE":       KeyCredentialSrc,
	"AWS_ROLE_SESSION_NAME":       KeyRoleSessionName,
	"AWS_EXTERNAL_ID":             KeyExternalID,
	"AWS_WEB_IDENTITY_TOKEN_FILE": KeyWebIdentityFile,
	"AWS_ENDPOINT_URL":            KeyEndpointURL,
	"AWS_CLI_PAGER":               KeyCLIPager,
	"AWS_RETRY_MODE":              KeyRetryMode,
	"AWS_MAX_ATTEMPTS":            KeyMaxAttempts,
	"AWS_STS_REGIONAL_ENDPOINTS":  KeySTSEndpoints,
	"AWS_SSO_SESSION":             KeySSOSession,
	"AWS_SSO_START_URL":           KeySSOStartURL,
	"AWS_SSO_REGION":              KeySSORegion,
	"AWS_SSO_ACCOUNT_ID":          KeySSOAccountID,
	"AWS_SSO_ROLE_NAME":           KeySSORoleName,
}

type ProfileSummary struct {
	Name string   `json:"name"`
	Keys []string `json:"keys"`
}

type Profile struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

type ParsedCredentials struct {
	ProfileName string            `json:"profileName,omitempty"`
	Fields      map[string]string `json:"fields"`
}

type lineKind int

const (
	lineRaw lineKind = iota
	lineKV
	lineBlank
)

type line struct {
	kind  lineKind
	raw   string
	key   string
	value string
}

type section struct {
	name  string
	lines []line
}

type fileModel struct {
	preamble []line
	sections []section
}

func DefaultPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AWS_SHARED_CREDENTIALS_FILE")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "credentials"), nil
}

func List(path string) ([]ProfileSummary, error) {
	model, err := readModel(path)
	if err != nil {
		return nil, err
	}

	profiles := make([]ProfileSummary, 0, len(model.sections))
	for _, sec := range model.sections {
		keys := make([]string, 0)
		for _, ln := range sec.lines {
			if ln.kind == lineKV {
				keys = append(keys, ln.key)
			}
		}
		profiles = append(profiles, ProfileSummary{Name: sec.name, Keys: keys})
	}
	return profiles, nil
}

func Get(path, name string) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Profile{}, errors.New("profile name is required")
	}

	model, err := readModel(path)
	if err != nil {
		return Profile{}, err
	}

	idx := model.findSection(name)
	if idx < 0 {
		return Profile{Name: name, Fields: map[string]string{}}, nil
	}
	return Profile{Name: model.sections[idx].name, Fields: model.sections[idx].fields()}, nil
}

func Save(path, name string, fields map[string]string) error {
	name = strings.TrimSpace(name)
	if err := validateProfileName(name); err != nil {
		return err
	}
	if fields == nil {
		fields = map[string]string{}
	}

	model, err := readModel(path)
	if err != nil {
		return err
	}

	normalized := normalizeFields(fields)
	if err := validateFields(normalized); err != nil {
		return err
	}
	idx := model.findSection(name)
	if idx < 0 {
		model.sections = append(model.sections, section{name: name})
		idx = len(model.sections) - 1
	}

	model.sections[idx].name = name
	model.sections[idx].applyFields(normalized)
	return writeModel(path, model)
}

func Delete(path, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name is required")
	}

	model, err := readModel(path)
	if err != nil {
		return err
	}

	idx := model.findSection(name)
	if idx < 0 {
		return nil
	}
	model.sections = append(model.sections[:idx], model.sections[idx+1:]...)
	return writeModel(path, model)
}

func Parse(text string) ParsedCredentials {
	fields := map[string]string{}
	var profileName string
	var currentSection string

	sectionRe := regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	assignRe := regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`)

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, ";") {
			continue
		}
		if match := sectionRe.FindStringSubmatch(raw); len(match) == 2 {
			currentSection = cleanProfileName(match[1])
			if profileName == "" {
				profileName = currentSection
			}
			continue
		}
		if match := assignRe.FindStringSubmatch(raw); len(match) == 3 {
			key := strings.TrimSpace(match[1])
			value := cleanValue(match[2])
			if strings.EqualFold(key, "AWS_PROFILE") {
				if profileName == "" {
					profileName = cleanProfileName(value)
				}
				continue
			}
			if mapped, ok := envToCredentialKey[strings.ToUpper(key)]; ok {
				fields[mapped] = value
				continue
			}
			lowerKey := strings.ToLower(key)
			fields[lowerKey] = value
		}
	}

	if profileName == "" && currentSection != "" {
		profileName = currentSection
	}
	return ParsedCredentials{ProfileName: profileName, Fields: fields}
}

func readModel(path string) (fileModel, error) {
	var model fileModel

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model, nil
		}
		return model, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	var current *section
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			model.sections = append(model.sections, section{name: name})
			current = &model.sections[len(model.sections)-1]
			continue
		}

		parsed := parseLine(raw)
		if current == nil {
			model.preamble = append(model.preamble, parsed)
		} else {
			current.lines = append(current.lines, parsed)
		}
	}
	if err := scanner.Err(); err != nil {
		return model, err
	}
	return model, nil
}

func parseLine(raw string) line {
	if strings.TrimSpace(raw) == "" {
		return line{kind: lineBlank, raw: raw}
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "#") || strings.HasPrefix(strings.TrimSpace(raw), ";") {
		return line{kind: lineRaw, raw: raw}
	}
	before, after, ok := strings.Cut(raw, "=")
	if !ok {
		return line{kind: lineRaw, raw: raw}
	}
	key := strings.ToLower(strings.TrimSpace(before))
	if key == "" {
		return line{kind: lineRaw, raw: raw}
	}
	return line{kind: lineKV, raw: raw, key: key, value: strings.TrimSpace(after)}
}

func (m fileModel) findSection(name string) int {
	for i, sec := range m.sections {
		if sec.name == name {
			return i
		}
	}
	return -1
}

func (s section) fields() map[string]string {
	fields := map[string]string{}
	for _, ln := range s.lines {
		if ln.kind == lineKV {
			fields[ln.key] = ln.value
		}
	}
	return fields
}

func (s *section) applyFields(fields map[string]string) {
	seen := map[string]bool{}
	for i := range s.lines {
		if s.lines[i].kind != lineKV {
			continue
		}
		key := s.lines[i].key
		value, ok := fields[key]
		if !ok {
			continue
		}
		if value == "" {
			s.lines[i].kind = lineRaw
			s.lines[i].raw = ""
			seen[key] = true
			continue
		}
		s.lines[i].value = value
		s.lines[i].raw = ""
		seen[key] = true
	}

	keys := make([]string, 0, len(fields))
	for key, value := range fields {
		if value == "" || seen[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s.lines = append(s.lines, line{kind: lineKV, key: key, value: fields[key]})
	}

	filtered := s.lines[:0]
	for _, ln := range s.lines {
		if ln.kind == lineRaw && ln.raw == "" {
			continue
		}
		filtered = append(filtered, ln)
	}
	s.lines = filtered
}

func normalizeFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.ToLower(strings.TrimSpace(key))
		key = strings.ReplaceAll(key, " ", "_")
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if mapped, ok := envToCredentialKey[strings.ToUpper(key)]; ok {
			key = mapped
		}
		out[key] = value
	}
	return out
}

func writeModel(path string, model fileModel) error {
	var buf bytes.Buffer
	writeLines(&buf, model.preamble)
	if len(model.preamble) > 0 && len(model.sections) > 0 {
		last := model.preamble[len(model.preamble)-1]
		if last.kind != lineBlank {
			buf.WriteByte('\n')
		}
	}
	for i, sec := range model.sections {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "[%s]\n", sec.name)
		writeLines(&buf, sec.lines)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := backupFile(path); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(buf.Bytes()); err != nil {
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

func writeLines(w io.Writer, lines []line) {
	for _, ln := range lines {
		switch ln.kind {
		case lineKV:
			if ln.raw != "" {
				fmt.Fprintln(w, ln.raw)
			} else {
				fmt.Fprintf(w, "%s = %s\n", ln.key, ln.value)
			}
		default:
			fmt.Fprintln(w, ln.raw)
		}
	}
}

func backupFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	backup := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return err
	}
	pruneBackups(path)
	return nil
}

const defaultMaxBackups = 10

// maxBackups is how many timestamped backups to retain. Override with
// EZCLOUD_MAX_BACKUPS; 0 disables pruning (unbounded, the old behavior).
func maxBackups() int {
	if v := strings.TrimSpace(os.Getenv("EZCLOUD_MAX_BACKUPS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultMaxBackups
}

// pruneBackups deletes the oldest "<path>.bak.*" files beyond the retention
// limit so plaintext-secret backups do not accumulate without bound. The
// timestamp suffix (YYYYMMDD-HHMMSS) sorts lexicographically by age, so the
// lexicographically smallest matches are the oldest.
func pruneBackups(path string) {
	limit := maxBackups()
	if limit <= 0 {
		return
	}
	matches, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(matches) <= limit {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-limit] {
		_ = os.Remove(old)
	}
}

// validateProfileName rejects names that would corrupt INI section headers or
// inject additional sections into the credentials file.
func validateProfileName(name string) error {
	if name == "" {
		return errors.New("profile name is required")
	}
	if strings.ContainsAny(name, "\n\r") {
		return errors.New("profile name must not contain line breaks")
	}
	if strings.ContainsAny(name, "[]") {
		return errors.New("profile name must not contain '[' or ']'")
	}
	return nil
}

// validateFields validates every key/value pair before it is written.
func validateFields(fields map[string]string) error {
	for key, value := range fields {
		if err := validateField(key, value); err != nil {
			return err
		}
	}
	return nil
}

// validKeyRe constrains field names to a safe INI identifier (keys are already
// lowercased by normalizeFields). This rejects not only structural characters
// ('[', ']', '=', whitespace, line breaks) but also comment prefixes ('#', ';')
// that would otherwise be written and then silently swallowed as a comment on
// the next read.
var validKeyRe = regexp.MustCompile(`^[a-z0-9_][a-z0-9_.-]*$`)

// validateField rejects keys/values that would break the "key = value" INI
// layout, inject extra keys/sections, or be silently dropped as a comment.
func validateField(key, value string) error {
	if key == "" {
		return errors.New("field name must not be empty")
	}
	if !validKeyRe.MatchString(key) {
		return fmt.Errorf("field name %q must use only lowercase letters, digits, '_', '.' or '-'", key)
	}
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("value for %q must not contain line breaks", key)
	}
	if hasControlChars(value) {
		return fmt.Errorf("value for %q must not contain control characters", key)
	}
	return nil
}

// hasControlChars reports whether s contains ASCII control characters other than
// a plain tab (defense-in-depth against smuggling terminators/escapes into the file).
func hasControlChars(s string) bool {
	for _, r := range s {
		if r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func cleanProfileName(value string) string {
	value = strings.TrimSpace(cleanValue(value))
	if strings.HasPrefix(strings.ToLower(value), "profile ") {
		value = strings.TrimSpace(value[len("profile "):])
	}
	return value
}

func cleanValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
		value = strings.TrimPrefix(strings.TrimSuffix(value, "\""), "\"")
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
		value = strings.TrimPrefix(strings.TrimSuffix(value, "'"), "'")
	}
	return value
}
