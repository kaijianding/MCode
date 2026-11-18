//go:build darwin

package accounts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareAccountKeychainReal(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	keychainsDir := filepath.Join(home, "Library", "Keychains")
	if err := os.MkdirAll(keychainsDir, 0o700); err != nil {
		t.Fatalf("mkdir keychains dir failed: %v", err)
	}

	keychainPath := filepath.Join(keychainsDir, "login.keychain-db")
	if err := prepareAccountKeychain(keychainPath); err != nil {
		t.Fatalf("prepareAccountKeychain first call failed: %v", err)
	}
	if _, err := os.Stat(keychainPath); err != nil {
		t.Fatalf("keychain file missing after create: %v", err)
	}

	// 再次调用应幂等成功（解锁已存在钥匙串）。
	if err := prepareAccountKeychain(keychainPath); err != nil {
		t.Fatalf("prepareAccountKeychain second call failed: %v", err)
	}

	resolvedPath, err := filepath.EvalSymlinks(keychainPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", keychainPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "default-keychain")
	environ := os.Environ()
	env := make([]string, 0, len(environ)+1)
	for _, e := range environ {
		if strings.HasPrefix(e, "HOME=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "HOME="+home)
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("security default-keychain failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), resolvedPath) {
		t.Fatalf("default-keychain = %q, want containing %q", strings.TrimSpace(string(out)), resolvedPath)
	}
}
