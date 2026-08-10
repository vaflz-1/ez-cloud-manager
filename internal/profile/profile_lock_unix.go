//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package profile

import (
	"os"
	"syscall"
)

// acquireProfileLock takes a blocking, advisory, cross-process lock on a
// stable per-profile file. The kernel releases it if the process exits.
func acquireProfileLock(root, id string) (func() error, error) {
	path, err := profileLockPath(root, id)
	if err != nil {
		return nil, err
	}
	return acquireAdvisoryLock(path)
}

// acquireRootLock serializes operations whose correctness depends on a
// root-wide snapshot. Callers that also need a profile lock must always take
// this lock first; that single order prevents root/profile lock inversion.
func acquireRootLock(root string) (func() error, error) {
	path, err := rootLockPath(root)
	if err != nil {
		return nil, err
	}
	return acquireAdvisoryLock(path)
}

func acquireAdvisoryLock(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
