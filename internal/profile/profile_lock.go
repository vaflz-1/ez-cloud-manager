package profile

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

const rootLockFilename = ".profiles.lock"

// rootLockPath returns the one stable lock inode that protects invariants
// spanning every profile in root (currently unique names and keeping at least
// one profile). Like the per-profile lock files, it is deliberately retained:
// unlinking it could split waiters across two inodes and break serialization.
func rootLockPath(root string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(root, rootLockFilename), nil
}

// profileLockPath returns a stable lock inode outside the profile directory.
// It must not be removed after use: unlinking a lock file while another process
// waits on its old inode could let a new opener acquire a different inode and
// enter the same critical section. List ignores this non-directory entry.
func profileLockPath(root, id string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(root, fmt.Sprintf(".profile-%x.lock", digest)), nil
}
