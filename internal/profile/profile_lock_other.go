//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package profile

import "sync"

// Platforms without stdlib syscall.Flock retain correct goroutine-level
// serialization, but not the cross-process guarantee of the Unix build.
// Both root-wide and per-profile lock paths share this map, preserving the
// same ordering and exclusion semantics within one process.
var fallbackProfileLocks sync.Map

func acquireProfileLock(root, id string) (func() error, error) {
	path, err := profileLockPath(root, id)
	if err != nil {
		return nil, err
	}
	return acquireFallbackLock(path), nil
}

func acquireRootLock(root string) (func() error, error) {
	path, err := rootLockPath(root)
	if err != nil {
		return nil, err
	}
	return acquireFallbackLock(path), nil
}

func acquireFallbackLock(path string) func() error {
	value, _ := fallbackProfileLocks.LoadOrStore(path, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return func() error {
		mutex.Unlock()
		return nil
	}
}
