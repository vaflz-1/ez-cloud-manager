package connectionsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"ez-cloud-manager/internal/provider"
)

const vendorOutputLimit = 1 << 20 // 1 MiB per stream

// Runner exists so tests can pin every argv/env pair without launching a
// browser or depending on locally-installed cloud SDKs.
type Runner interface {
	Run(ctx context.Context, executable string, args, env []string) ([]byte, error)
}

type ExecRunner struct{}

var errVendorCLI = errors.New("vendor CLI failed")

func (ExecRunner) Run(ctx context.Context, executable string, args, env []string) ([]byte, error) {
	path, err := lookPathInEnvironment(executable, env)
	if err != nil {
		return nil, fmt.Errorf("%w: %s CLI is unavailable: %v", errVendorCLI, executable, err)
	}
	env = vendorExecutionEnvironment(env, path)
	stdout := provider.NewCappedBuffer(vendorOutputLimit)
	stderr := provider.NewCappedBuffer(vendorOutputLimit)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append([]string(nil), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	// Browser-capable CLIs can leave helper children behind. Kill the process
	// group on cancellation instead of only the immediate parent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if stdout.Exceeded() || stderr.Exceeded() {
			return nil, fmt.Errorf("%w: %s CLI output exceeded the 1 MiB safety limit", errVendorCLI, executable)
		}
		// Vendor stderr may contain a device code, URL, account identifier, or
		// other sensitive material. It is intentionally not copied into errors.
		return nil, fmt.Errorf("%w: %s CLI exited unsuccessfully", errVendorCLI, executable)
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, fmt.Errorf("%w: %s CLI output exceeded the 1 MiB safety limit", errVendorCLI, executable)
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

// exec.LookPath consults the current process PATH, not cmd.Env. Resolve from
// the same sanitized environment the child will receive so a caller cannot
// accidentally execute a binary from a path the vendor CLI itself cannot see.
func lookPathInEnvironment(executable string, env []string) (string, error) {
	if executable == "" || filepath.Base(executable) != executable || strings.ContainsAny(executable, `/\\`) {
		return "", fmt.Errorf("vendor executable must be a bare name")
	}
	pathValue := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
			break
		}
	}
	if pathValue == "" {
		return "", exec.ErrNotFound
	}
	var unsafeFound bool
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" || !filepath.IsAbs(directory) {
			unsafeFound = true
			continue
		}
		if !trustedSearchDirectory(directory) {
			unsafeFound = true
			continue
		}
		protectedSymlinkRequired := searchDirectoryAllowsGroupReplacement(directory)
		candidate := filepath.Join(directory, executable)
		info, err := os.Lstat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if !trustedPathOwner(info) ||
			(!isSymlink && (info.Mode().Perm()&0o022 != 0 || info.Mode()&0o111 == 0)) {
			unsafeFound = true
			continue
		}
		if protectedSymlinkRequired && !isSymlink {
			unsafeFound = true
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || !filepath.IsAbs(resolved) ||
			(protectedSymlinkRequired && filepath.Base(resolved) != executable) ||
			!trustedExecutablePath(resolved) {
			unsafeFound = true
			continue
		}
		return resolved, nil
	}
	if unsafeFound {
		return "", fmt.Errorf("vendor executable location is unsafe")
	}
	return "", exec.ErrNotFound
}

// A group-writable search path is compatible with standard macOS package
// managers only when the candidate is a symlink into a protected install tree.
// A direct executable in that path can be replaced between validation and
// exec by another member of the directory's group.
func searchDirectoryAllowsGroupReplacement(path string) bool {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return true
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		rootSticky := ok && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0o020 != 0 && !rootSticky {
			return true
		}
		next := filepath.Dir(current)
		if next == current {
			return false
		}
	}
}

func trustedSearchDirectory(path string) bool {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil || !trustedDirectory(info) {
			return false
		}
		next := filepath.Dir(current)
		if next == current {
			return true
		}
	}
}

func trustedDirectory(info os.FileInfo) bool {
	if !info.IsDir() || !trustedPathOwner(info) {
		return false
	}
	if info.Mode().Perm()&0o002 == 0 {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
}

func trustedPathOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || int(stat.Uid) == os.Geteuid())
}

// trustedExecutablePath rejects a vendor binary or resolved parent directory
// that another local account can replace. Executables and their resolved tree
// may not be group/world writable (apart from root-owned sticky ancestors),
// and every component must belong to the current user or root. Executing the
// resolved target prevents the discovery symlink from being retargeted after
// validation.
func trustedExecutablePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	if !trustedPathOwner(info) {
		return false
	}
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil || !trustedResolvedDirectory(info) {
			return false
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
	}
	return true
}

func trustedResolvedDirectory(info os.FileInfo) bool {
	if !info.IsDir() || !trustedPathOwner(info) {
		return false
	}
	if info.Mode().Perm()&0o022 == 0 {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
}

func vendorExecutionEnvironment(env []string, resolvedPath string) []string {
	safePath := strings.Join([]string{filepath.Dir(resolvedPath), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "PATH="+safePath)
}

var allowedEnvironmentKeys = map[string]bool{
	"HOME":                    true,
	"LANG":                    true,
	"LC_ALL":                  true,
	"LC_CTYPE":                true,
	"LOGNAME":                 true,
	"PATH":                    true,
	"SSH_AUTH_SOCK":           true,
	"TMPDIR":                  true,
	"TZ":                      true,
	"USER":                    true,
	"__CF_USER_TEXT_ENCODING": true,
}

// vendorEnvironment strips ambient cloud credentials/config overrides. The
// caller adds only the single provider config root required for the action.
func vendorEnvironment(overrides map[string]string) []string {
	values := make(map[string]string, len(allowedEnvironmentKeys)+len(overrides))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && allowedEnvironmentKeys[key] {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
