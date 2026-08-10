package audit

// auditLockPath returns the stable lock file that serializes rotation and
// append for one audit log. The lock is deliberately retained next to the
// log: unlinking it could split waiters across different inodes and allow two
// processes into the same critical section.
func auditLockPath(path string) string {
	return path + ".lock"
}
