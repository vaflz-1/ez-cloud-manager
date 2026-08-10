//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package audit

import "sync"

// Platforms without syscall.Flock retain goroutine-level serialization. The
// stable path key preserves the same exclusion semantics within one process.
var fallbackAuditLocks sync.Map

func acquireAuditLock(path string) (func() error, error) {
	value, _ := fallbackAuditLocks.LoadOrStore(auditLockPath(path), &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return func() error {
		mutex.Unlock()
		return nil
	}, nil
}
