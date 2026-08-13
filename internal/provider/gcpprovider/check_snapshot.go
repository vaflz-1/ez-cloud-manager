package gcpprovider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"ez-cloud-manager/internal/gcpcreds"
	"ez-cloud-manager/internal/inifile"
)

var checkGCPProjectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

type gcpCheckSnapshot struct {
	name    string
	path    string
	account string
	project string
}

func (s *gcpCheckSnapshot) cleanup() error {
	if s.path == "" {
		return nil
	}
	path := s.path
	s.path = ""
	var result error
	for _, candidate := range []string{path, path + ".lock"} {
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

// prepareGCPCheckSnapshot creates a non-active gcloud configuration containing
// only the captured principal and project. The credential database remains in
// the vendor-owned root, but auth/impersonation/proxy/endpoint fields from the
// mutable source configuration are never passed to gcloud.
func prepareGCPCheckSnapshot(root string, fields map[string]string) (gcpCheckSnapshot, error) {
	account := strings.TrimSpace(fields[gcpcreds.KeyAccount])
	project := strings.TrimSpace(fields[gcpcreds.KeyProject])
	if !safeGCPCheckValue(account, 1024) || strings.HasPrefix(account, "-") {
		return gcpCheckSnapshot{}, errors.New("gcloud configuration has no safe account")
	}
	if project != "" && !checkGCPProjectID.MatchString(project) {
		return gcpCheckSnapshot{}, errors.New("gcloud configuration has an invalid project ID")
	}
	configurations := filepath.Join(root, "configurations")
	if err := os.MkdirAll(configurations, 0o700); err != nil {
		return gcpCheckSnapshot{}, fmt.Errorf("prepare isolated gcloud verification: %w", err)
	}
	file, err := os.CreateTemp(configurations, "config_kervik-check-")
	if err != nil {
		return gcpCheckSnapshot{}, fmt.Errorf("create isolated gcloud verification: %w", err)
	}
	snapshot := gcpCheckSnapshot{
		name: strings.TrimPrefix(filepath.Base(file.Name()), "config_"),
		path: file.Name(), account: account, project: project,
	}
	model := inifile.Model{Sections: []inifile.Section{{Name: "core"}}}
	model.Sections[0].ApplyFields(map[string]string{
		"account":                 account,
		"project":                 project,
		"disable_usage_reporting": "true",
	})
	if _, err := file.Write(inifile.Render(model)); err != nil {
		_ = file.Close()
		_ = snapshot.cleanup()
		return gcpCheckSnapshot{}, fmt.Errorf("write isolated gcloud verification: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = snapshot.cleanup()
		return gcpCheckSnapshot{}, fmt.Errorf("secure isolated gcloud verification: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = snapshot.cleanup()
		return gcpCheckSnapshot{}, fmt.Errorf("close isolated gcloud verification: %w", err)
	}
	return snapshot, nil
}

func safeGCPCheckValue(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || strings.ContainsRune(value, 0) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
