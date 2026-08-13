package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVendorEnvironmentStripsCredentialRoutingAndInterpreterState(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "must-not-propagate")
	t.Setenv("HTTPS_PROXY", "https://attacker.invalid")
	t.Setenv("SSL_CERT_FILE", "/tmp/attacker.pem")
	t.Setenv("PYTHONPATH", "/tmp/attacker-python")
	t.Setenv("PATH", "/usr/bin:/bin")
	env := VendorEnvironment(map[string]string{"AWS_CONFIG_FILE": "/isolated/config"})
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"AWS_ACCESS_KEY_ID", "HTTPS_PROXY", "SSL_CERT_FILE", "PYTHONPATH"} {
		if strings.Contains(joined, forbidden+"=") {
			t.Fatalf("%s leaked into vendor environment: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "AWS_CONFIG_FILE=/isolated/config") {
		t.Fatalf("explicit isolated config missing: %s", joined)
	}
}

func TestVendorExecutableRejectsDirectBinaryInGroupWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "fakecloud")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveVendorExecutable("fakecloud", []string{"PATH=" + binDir}); err == nil {
		t.Fatalf("direct binary in group-writable search directory was accepted: %q", got)
	}
}

func TestVendorExecutableAllowsOwnedSymlinkFromOwnedGroupWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(t.TempDir(), "trusted")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "fakecloud")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "fakecloud")); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveVendorExecutable("fakecloud", []string{"PATH=" + binDir})
	if err != nil || got != want {
		t.Fatalf("owned Homebrew-style symlink in %q was rejected: path=%q err=%v", binDir, got, err)
	}
}

func TestVendorExecutableRejectsRenamedTargetFromGroupWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/env", filepath.Join(binDir, "fakecloud")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveVendorExecutable("fakecloud", []string{"PATH=" + binDir}); err == nil {
		t.Fatal("renamed trusted target from group-writable search directory was accepted")
	}
}

func TestVendorExecutionEnvironmentDropsDiscoveryPath(t *testing.T) {
	env := vendorExecutionEnvironment([]string{"HOME=/tmp/home", "PATH=/untrusted/bin:/usr/bin"}, "/trusted/aws")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "/untrusted/bin") || !strings.Contains(joined, "PATH=/trusted:/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Fatalf("unexpected execution environment: %s", joined)
	}
}

func TestVendorExecutableRejectsWorldWritableSearchDirectory(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "fakecloud")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveVendorExecutable("fakecloud", []string{"PATH=" + binDir}); err == nil {
		t.Fatal("world-writable search directory was accepted")
	}
}

func TestResolveInstalledAWSWhenPresent(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/aws"); err != nil {
		t.Skip("AWS CLI is not installed at the standard macOS path")
	}
	got, err := resolveVendorExecutable("aws", []string{"PATH=/usr/local/bin:/usr/bin:/bin"})
	if err != nil || got == "" {
		t.Fatalf("standard AWS CLI installation was rejected: path=%q err=%v", got, err)
	}
}
