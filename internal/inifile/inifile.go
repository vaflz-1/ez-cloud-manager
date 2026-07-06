// Package inifile is the shared INI engine for provider backends: a
// line-preserving section model plus atomic, backed-up persistence.
//
// It generalizes the storage layer proven in internal/awscreds so new
// backends (Azure profiles, gcloud configurations) inherit the same safety
// properties — atomic temp-file+rename writes, 0600 permissions, timestamped
// backups with pruning, and injection-safe validation — without re-deriving
// them. awscreds keeps its own private copy on purpose: its on-disk behavior
// is pinned by tests and must not change underneath existing AWS users.
package inifile

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

type LineKind int

const (
	LineRaw LineKind = iota
	LineKV
	LineBlank
)

// Line is one physical line: either an opaque raw line (comments, unparsable
// content) or a key/value assignment. Raw text is preserved on rewrite.
type Line struct {
	Kind  LineKind
	Raw   string
	Key   string
	Value string
}

// Section is a named "[header]" block and the lines under it.
type Section struct {
	Name  string
	Lines []Line
}

// Model is a whole file: lines before the first section, then sections.
type Model struct {
	Preamble []Line
	Sections []Section
}

// Read loads path into a Model. A missing file is an empty model, so callers
// can treat "no file yet" and "empty file" identically.
func Read(path string) (Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Model{}, nil
		}
		return Model{}, err
	}
	return ReadBytes(data), nil
}

// ReadBytes parses raw file content into a Model.
func ReadBytes(data []byte) Model {
	var model Model
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var current *Section
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			model.Sections = append(model.Sections, Section{Name: name})
			current = &model.Sections[len(model.Sections)-1]
			continue
		}
		parsed := parseLine(raw)
		if current == nil {
			model.Preamble = append(model.Preamble, parsed)
		} else {
			current.Lines = append(current.Lines, parsed)
		}
	}
	return model
}

func parseLine(raw string) Line {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Line{Kind: LineBlank, Raw: raw}
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
		return Line{Kind: LineRaw, Raw: raw}
	}
	before, after, ok := strings.Cut(raw, "=")
	if !ok {
		return Line{Kind: LineRaw, Raw: raw}
	}
	key := strings.ToLower(strings.TrimSpace(before))
	if key == "" {
		return Line{Kind: LineRaw, Raw: raw}
	}
	return Line{Kind: LineKV, Raw: raw, Key: key, Value: strings.TrimSpace(after)}
}

// FindSection returns the index of the named section, or -1.
func (m Model) FindSection(name string) int {
	for i, sec := range m.Sections {
		if sec.Name == name {
			return i
		}
	}
	return -1
}

// DeleteSection removes the named section; it reports whether it was present.
func (m *Model) DeleteSection(name string) bool {
	idx := m.FindSection(name)
	if idx < 0 {
		return false
	}
	m.Sections = append(m.Sections[:idx], m.Sections[idx+1:]...)
	return true
}

// Fields returns the section's key/value pairs.
func (s Section) Fields() map[string]string {
	fields := map[string]string{}
	for _, ln := range s.Lines {
		if ln.Kind == LineKV {
			fields[ln.Key] = ln.Value
		}
	}
	return fields
}

// ApplyFields updates the section in place: existing keys are rewritten (an
// empty value deletes the line), new non-empty keys are appended sorted.
// Untouched raw/comment lines are preserved.
func (s *Section) ApplyFields(fields map[string]string) {
	seen := map[string]bool{}
	for i := range s.Lines {
		if s.Lines[i].Kind != LineKV {
			continue
		}
		key := s.Lines[i].Key
		value, ok := fields[key]
		if !ok {
			continue
		}
		if value == "" {
			s.Lines[i].Kind = LineRaw
			s.Lines[i].Raw = ""
			seen[key] = true
			continue
		}
		s.Lines[i].Value = value
		s.Lines[i].Raw = ""
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
		s.Lines = append(s.Lines, Line{Kind: LineKV, Key: key, Value: fields[key]})
	}

	filtered := s.Lines[:0]
	for _, ln := range s.Lines {
		if ln.Kind == LineRaw && ln.Raw == "" {
			continue
		}
		filtered = append(filtered, ln)
	}
	s.Lines = filtered
}

// Render serializes the model back to file bytes.
func Render(m Model) []byte {
	var buf bytes.Buffer
	writeLines(&buf, m.Preamble)
	if len(m.Preamble) > 0 && len(m.Sections) > 0 {
		last := m.Preamble[len(m.Preamble)-1]
		if last.Kind != LineBlank {
			buf.WriteByte('\n')
		}
	}
	for i, sec := range m.Sections {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "[%s]\n", sec.Name)
		writeLines(&buf, sec.Lines)
	}
	return buf.Bytes()
}

func writeLines(w io.Writer, lines []Line) {
	for _, ln := range lines {
		switch ln.Kind {
		case LineKV:
			if ln.Raw != "" {
				fmt.Fprintln(w, ln.Raw)
			} else {
				fmt.Fprintf(w, "%s = %s\n", ln.Key, ln.Value)
			}
		default:
			fmt.Fprintln(w, ln.Raw)
		}
	}
}

// WriteAtomic persists the model: parent dir 0700, optional timestamped
// backup of the previous content (pruned to a retention limit), then a 0600
// temp file renamed over path so readers never observe a partial file.
func WriteAtomic(path string, m Model, backup bool) error {
	return WriteFileAtomic(path, Render(m), backup)
}

// WriteFileAtomic is WriteAtomic for pre-rendered bytes (non-INI callers).
func WriteFileAtomic(path string, data []byte, backup bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if backup {
		if err := backupFile(path); err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
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

// maxBackups mirrors awscreds: EZCLOUD_MAX_BACKUPS overrides retention,
// 0 disables pruning.
func maxBackups() int {
	if v := strings.TrimSpace(os.Getenv("EZCLOUD_MAX_BACKUPS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultMaxBackups
}

// pruneBackups deletes the oldest "<path>.bak.*" beyond retention; the
// timestamp suffix sorts lexicographically by age.
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

// ValidateSectionName rejects names that would corrupt "[header]" lines or
// inject additional sections.
func ValidateSectionName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.ContainsAny(name, "\n\r") {
		return errors.New("name must not contain line breaks")
	}
	if strings.ContainsAny(name, "[]") {
		return errors.New("name must not contain '[' or ']'")
	}
	return nil
}

// validKeyRe constrains keys to safe INI identifiers (lowercased by callers):
// structural characters and comment prefixes are rejected so nothing written
// can be swallowed or re-interpreted on the next read.
var validKeyRe = regexp.MustCompile(`^[a-z0-9_][a-z0-9_.-]*$`)

// ValidateField rejects keys/values that would break "key = value" layout,
// inject extra keys/sections, or be silently dropped as a comment.
func ValidateField(key, value string) error {
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

// hasControlChars reports ASCII control characters other than plain tab
// (defense-in-depth against smuggling terminators/escapes into the file).
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
