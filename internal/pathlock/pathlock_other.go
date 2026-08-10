//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package pathlock

import "sync"

// Platforms without stdlib syscall.Flock retain process-local serialization.
// The ordered multi-path acquisition in pathlock.go preserves the same
// deadlock-free semantics, but cannot coordinate independent processes.
var fallbackLocks sync.Map

func acquireFileLock(path string) (Release, error) {
	value, _ := fallbackLocks.LoadOrStore(path, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return func() error {
		mutex.Unlock()
		return nil
	}, nil
}
