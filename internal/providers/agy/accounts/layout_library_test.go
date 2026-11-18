package accounts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolateLibrary(t *testing.T) {
	oldGoos := goos
	t.Cleanup(func() { goos = oldGoos })
	goos = "darwin"

	setupHostLibrary := func(t *testing.T) (string, string) {
		t.Helper()
		hostHome := t.TempDir()
		hostLib := filepath.Join(hostHome, "Library")
		for _, sub := range []string{"Keychains", "Preferences", "Application Support"} {
			if err := os.MkdirAll(filepath.Join(hostLib, sub), 0o700); err != nil {
				t.Fatalf("mkdir host Library/%s failed: %v", sub, err)
			}
		}
		return hostHome, hostLib
	}

	assertIsolatedLibrary := func(t *testing.T, home, hostLib string) {
		t.Helper()
		homeLib := filepath.Join(home, "Library")
		info, err := os.Lstat(homeLib)
		if err != nil {
			t.Fatalf("lstat home Library failed: %v", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			t.Fatalf("home/Library 应为真实目录，实际 mode=%v isDir=%v", info.Mode(), info.IsDir())
		}

		for _, name := range []string{"Preferences", "Application Support"} {
			linkPath := filepath.Join(homeLib, name)
			fi, err := os.Lstat(linkPath)
			if err != nil {
				t.Fatalf("lstat Library/%s failed: %v", name, err)
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("Library/%s 应为软链接，实际 mode=%v", name, fi.Mode())
			}
			target, err := os.Readlink(linkPath)
			if err != nil {
				t.Fatalf("readlink Library/%s failed: %v", name, err)
			}
			want := filepath.Join(hostLib, name)
			if target != want {
				t.Fatalf("Library/%s target = %q, want %q", name, target, want)
			}
		}

		if _, err := os.Lstat(filepath.Join(homeLib, "Keychains")); !os.IsNotExist(err) {
			t.Fatalf("home/Library/Keychains 不应存在，实际 err=%v", err)
		}
	}

	t.Run("new_account", func(t *testing.T) {
		hostHome, hostLib := setupHostLibrary(t)
		mgr, err := NewManager(Options{DataDir: t.TempDir(), HostHome: hostHome})
		if err != nil {
			t.Fatalf("NewManager failed: %v", err)
		}

		const id = "iso-new"
		if err := mgr.setupAccountLayout(id); err != nil {
			t.Fatalf("setupAccountLayout failed: %v", err)
		}

		assertIsolatedLibrary(t, mgr.accountHomeDir(id), hostLib)
	})

	t.Run("existing_symlink_migration", func(t *testing.T) {
		hostHome, hostLib := setupHostLibrary(t)
		mgr, err := NewManager(Options{DataDir: t.TempDir(), HostHome: hostHome})
		if err != nil {
			t.Fatalf("NewManager failed: %v", err)
		}

		const id = "iso-legacy"
		home := mgr.accountHomeDir(id)
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatalf("mkdir home failed: %v", err)
		}
		if err := os.Symlink(hostLib, filepath.Join(home, "Library")); err != nil {
			t.Fatalf("symlink legacy Library failed: %v", err)
		}

		if _, _, _, err := mgr.buildRuntimeEnv(id); err != nil {
			t.Fatalf("buildRuntimeEnv failed: %v", err)
		}

		assertIsolatedLibrary(t, home, hostLib)
		for _, sub := range []string{"Keychains", "Preferences"} {
			if _, err := os.Stat(filepath.Join(hostLib, sub)); err != nil {
				t.Fatalf("宿主 Library/%s 应仍然存在: %v", sub, err)
			}
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		hostHome, hostLib := setupHostLibrary(t)
		mgr, err := NewManager(Options{DataDir: t.TempDir(), HostHome: hostHome})
		if err != nil {
			t.Fatalf("NewManager failed: %v", err)
		}

		const id = "iso-repeat"
		if err := mgr.setupAccountLayout(id); err != nil {
			t.Fatalf("first setupAccountLayout failed: %v", err)
		}
		if _, _, _, err := mgr.buildRuntimeEnv(id); err != nil {
			t.Fatalf("first buildRuntimeEnv failed: %v", err)
		}
		if _, _, _, err := mgr.buildRuntimeEnv(id); err != nil {
			t.Fatalf("second buildRuntimeEnv failed: %v", err)
		}

		assertIsolatedLibrary(t, mgr.accountHomeDir(id), hostLib)
	})
}
