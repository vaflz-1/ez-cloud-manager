// Package audit provides an append-only, REDACTED audit log of important
// configuration changes (credential saves, deletes, renames, and similar).
//
// SECURITY INVARIANT: only key NAMES and metadata are ever recorded — never
// field VALUES. An audit entry may say "the aws_secret_access_key of profile
// prod changed", but must never contain the secret itself. Callers uphold
// this by populating Event.Keys with key names only (see ChangedKeys, which
// compares values but returns names). This package never reads or logs a
// value from a caller's field map; it only marshals the Event it is given.
//
// The log is a stream of one JSON object per line. It is written 0600 and
// size-capped so it cannot grow without bound.
package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxLogBytes is the best-effort size cap. When the log exceeds this before a
// write, it is rotated to "<path>.1" (replacing any previous rotation). One
// generation of history is kept; this is an operator convenience, not a
// tamper-proof audit trail.
const maxLogBytes = 1 << 20 // 1 MiB

// Event is a single audit record. Keys holds the NAMES of the fields that
// changed — never their values. Detail is free-form context and must not be
// used to smuggle secret values.
type Event struct {
	Time     string   `json:"time"`
	Action   string   `json:"action"`
	Provider string   `json:"provider,omitempty"`
	Profile  string   `json:"profile,omitempty"`
	Keys     []string `json:"keys,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// DefaultPath returns the audit.log location. EZCLOUD_CONFIG_DIR overrides
// the directory; otherwise it falls back to ~/.config/ezcloud.
func DefaultPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("EZCLOUD_CONFIG_DIR")); override != "" {
		return filepath.Join(override, "audit.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ezcloud", "audit.log"), nil
}

// Append writes one Event as a single JSON line. Time is filled with the
// current UTC time (RFC3339) when empty, and Keys are sorted and deduplicated
// for stable, readable output. The file is created or repaired to 0600; the
// directory is 0700. If the log already exceeds maxLogBytes it is rotated
// first.
func Append(path string, e Event) (err error) {
	if strings.TrimSpace(e.Time) == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	e.Keys = sortDedupe(e.Keys)

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	release, err := acquireAuditLock(path)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()

	if err := rotateIfLarge(path); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// List returns up to the last `limit` events in chronological order (newest
// LAST). A limit of zero or less returns all events. Malformed lines are
// skipped silently so a single corrupt entry never hides the rest of the log.
// A missing file yields an empty slice, not an error.
func List(path string, limit int) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Event{}, nil
		}
		return nil, err
	}

	events := make([]Event, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Allow long lines; a single event stays well under the log's 1 MiB cap.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogBytes)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

// ChangedKeys returns the sorted NAMES of keys that were added, removed, or
// whose value changed between two field maps. Values are compared to decide
// what changed but are never returned — this is what lets callers log which
// fields changed without leaking the secrets themselves.
func ChangedKeys(oldFields, newFields map[string]string) []string {
	changed := make(map[string]bool)
	for k, v := range newFields {
		if ov, ok := oldFields[k]; !ok || ov != v {
			changed[k] = true
		}
	}
	for k := range oldFields {
		if _, ok := newFields[k]; !ok {
			changed[k] = true
		}
	}

	keys := make([]string, 0, len(changed))
	for k := range changed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// rotateIfLarge moves an oversized log to "<path>.1", replacing any previous
// rotation, so the active log starts fresh. A missing log is fine (nothing to
// rotate).
func rotateIfLarge(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() <= maxLogBytes {
		return nil
	}

	rotated := path + ".1"
	if err := os.Remove(rotated); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(path, rotated)
}

// sortDedupe returns a sorted copy of in with duplicates removed. An empty
// input returns nil so Event.Keys stays omitted (omitempty) rather than
// serializing an empty array.
func sortDedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
