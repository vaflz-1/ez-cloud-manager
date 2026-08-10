//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package pathlock

import (
	"fmt"
	"os"
	"syscall"
)

// acquireFileLock takes a blocking advisory lock on a stable lock inode. The
// file is deliberately retained after release: unlinking it could split
// waiters between old and new inodes and break serialization.
func acquireFileLock(path string) (Release, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open storage lock %q: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure storage lock %q: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock storage path %q: %w", path, err)
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock storage path %q: %w", path, unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close storage lock %q: %w", path, closeErr)
		}
		return nil
	}, nil
}
