//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package audit

import (
	"os"
	"syscall"
)

// acquireAuditLock takes a blocking advisory lock on a stable file adjacent
// to the audit log. Keeping the lock across rotation and append makes that
// lifecycle atomic with respect to other application and add-on processes.
func acquireAuditLock(path string) (func() error, error) {
	file, err := os.OpenFile(auditLockPath(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
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
