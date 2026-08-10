// Package pathlock serializes read-modify-write transactions by logical
// storage path. Lock files are stable siblings of the protected files: the
// protected file itself cannot be locked because atomic replacement changes
// its inode.
package pathlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const lockSuffix = ".ezcloud.lock"

// Release releases every lock acquired by Acquire.
type Release func() error

// Acquire takes exclusive locks for paths in a deterministic order. Paths are
// canonicalized and deduplicated before acquisition, so callers can safely
// compose transactions spanning multiple storage files without lock-order
// inversion. The returned release function must be called exactly once.
func Acquire(paths ...string) (Release, error) {
	locks, err := orderedLockPaths(paths)
	if err != nil {
		return nil, err
	}

	releases := make([]Release, 0, len(locks))
	for _, lock := range locks {
		release, err := acquireFileLock(lock)
		if err != nil {
			return nil, errors.Join(err, releaseReverse(releases))
		}
		releases = append(releases, release)
	}

	return func() error {
		return releaseReverse(releases)
	}, nil
}

func orderedLockPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("at least one storage path is required")
	}

	seen := make(map[string]struct{}, len(paths))
	locks := make([]string, 0, len(paths))
	for _, path := range paths {
		lock, err := lockPath(path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[lock]; exists {
			continue
		}
		seen[lock] = struct{}{}
		locks = append(locks, lock)
	}
	sort.Strings(locks)
	return locks, nil
}

func lockPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("storage path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve storage path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create storage directory: %w", err)
	}
	// Resolve an existing symlinked parent so two aliases of the same storage
	// directory converge on the same lock file.
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolved
	}
	return filepath.Join(parent, "."+filepath.Base(absolute)+lockSuffix), nil
}

func releaseReverse(releases []Release) error {
	var result error
	for i := len(releases) - 1; i >= 0; i-- {
		result = errors.Join(result, releases[i]())
	}
	return result
}
