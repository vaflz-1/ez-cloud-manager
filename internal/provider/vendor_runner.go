package provider

import (
	"bytes"
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
)

// VendorCommandResult is bounded output from a trusted cloud CLI invocation.
// Callers must not expose Stderr directly: it can contain account metadata or
// short-lived authentication material.
type VendorCommandResult struct {
	Stdout []byte
}

// RunVendorCommand executes an exact argv against an allowlisted environment,
// resolves PATH from that same environment, bounds both streams, and kills the
// complete child process group on cancellation.
func RunVendorCommand(
	ctx context.Context,
	executable string,
	args []string,
	overrides map[string]string,
	outputLimit int,
) (VendorCommandResult, error) {
	return RunVendorCommandWithInput(ctx, executable, args, overrides, nil, outputLimit)
}

// RunVendorCommandWithInput is RunVendorCommand with a bounded caller-owned
// stdin payload. It exists for connector operations that already keep secret
// payloads off argv; interactive auth does not use it.
func RunVendorCommandWithInput(
	ctx context.Context,
	executable string,
	args []string,
	overrides map[string]string,
	input []byte,
	outputLimit int,
) (VendorCommandResult, error) {
	if outputLimit <= 0 {
		return VendorCommandResult{}, fmt.Errorf("vendor CLI output limit must be positive")
	}
	if len(input) > outputLimit {
		return VendorCommandResult{}, fmt.Errorf("vendor CLI input exceeded the safety limit")
	}
	env := VendorEnvironment(overrides)
	path, err := resolveVendorExecutable(executable, env)
	if err != nil {
		return VendorCommandResult{}, err
	}
	env = vendorExecutionEnvironment(env, path)
	stdout := NewCappedBuffer(outputLimit)
	stderr := NewCappedBuffer(outputLimit)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
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
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return VendorCommandResult{}, ctx.Err()
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return VendorCommandResult{}, fmt.Errorf("%s CLI output exceeded the safety limit", executable)
	}
	result := VendorCommandResult{
		Stdout: append([]byte(nil), stdout.Bytes()...),
	}
	if runErr != nil {
		return result, fmt.Errorf("%s CLI exited unsuccessfully", executable)
	}
	return result, nil
}

var vendorEnvironmentKeys = map[string]bool{
	"HOME": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true,
	"LOGNAME": true, "PATH": true, "SSH_AUTH_SOCK": true, "TMPDIR": true,
	"TZ": true, "USER": true, "__CF_USER_TEXT_ENCODING": true,
}

// VendorEnvironment strips ambient cloud credentials, proxy/CA routing and
// interpreter injection variables. Only explicit per-request overrides may
// add provider configuration paths.
func VendorEnvironment(overrides map[string]string) []string {
	values := make(map[string]string, len(vendorEnvironmentKeys)+len(overrides))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && vendorEnvironmentKeys[key] {
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

func resolveVendorExecutable(executable string, env []string) (string, error) {
	if filepath.IsAbs(executable) {
		resolved, err := filepath.EvalSymlinks(executable)
		if err != nil || !trustedVendorExecutablePath(resolved) {
			return "", fmt.Errorf("vendor CLI was found only in an unsafe location")
		}
		return resolved, nil
	}
	if executable == "" || filepath.Base(executable) != executable || strings.ContainsAny(executable, `/\\`) {
		return "", fmt.Errorf("invalid vendor CLI name")
	}
	pathValue := ""
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			pathValue = value
			break
		}
	}
	if pathValue == "" {
		return "", fmt.Errorf("%s CLI not found in the trusted PATH", executable)
	}
	var unsafeFound bool
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" || !filepath.IsAbs(directory) {
			unsafeFound = true
			continue
		}
		if !trustedVendorSearchDirectory(directory) {
			unsafeFound = true
			continue
		}
		protectedSymlinkRequired := vendorSearchDirectoryAllowsGroupReplacement(directory)
		candidate := filepath.Join(directory, executable)
		info, err := os.Lstat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if !trustedVendorPathOwner(info) ||
			(!isSymlink && (info.Mode().Perm()&0o022 != 0 || info.Mode()&0o111 == 0)) {
			unsafeFound = true
			continue
		}
		if protectedSymlinkRequired && !isSymlink {
			unsafeFound = true
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil ||
			(protectedSymlinkRequired && filepath.Base(resolved) != executable) ||
			!trustedVendorExecutablePath(resolved) {
			unsafeFound = true
			continue
		}
		return resolved, nil
	}
	if unsafeFound {
		return "", fmt.Errorf("%s CLI was found only in an unsafe location", executable)
	}
	return "", fmt.Errorf("%s CLI not found in the trusted PATH", executable)
}

func vendorSearchDirectoryAllowsGroupReplacement(path string) bool {
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

func trustedVendorSearchDirectory(path string) bool {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil || !trustedVendorDirectory(info) {
			return false
		}
		next := filepath.Dir(current)
		if next == current {
			return true
		}
	}
}

func trustedVendorDirectory(info os.FileInfo) bool {
	if !info.IsDir() || !trustedVendorPathOwner(info) {
		return false
	}
	if info.Mode().Perm()&0o002 == 0 {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
}

func trustedVendorPathOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || int(stat.Uid) == os.Geteuid())
}

func trustedVendorExecutablePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	if !trustedVendorPathOwner(info) {
		return false
	}
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil || !trustedVendorResolvedDirectory(info) {
			return false
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
	}
	return true
}

func trustedVendorResolvedDirectory(info os.FileInfo) bool {
	if !info.IsDir() || !trustedVendorPathOwner(info) {
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
