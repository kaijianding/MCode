package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"mcode/internal/fsutil"
	"mcode/internal/procattr"
)

// macOnlyLinks 是只在 macOS 上存在的宿主配置项，Windows 上没有共享意义。
var macOnlyLinks = map[string]bool{
	".zshrc": true,
}

// accountDir 返回该隔离账号的根目录 <dataDir>/agy-accounts/<id>。
func (m *Manager) accountDir(id string) string {
	return filepath.Join(m.opts.DataDir, "agy-accounts", id)
}

// accountHomeDir 返回隔离环境作为 HOME 的子目录 <dataDir>/agy-accounts/<id>/home。
func (m *Manager) accountHomeDir(id string) string {
	return filepath.Join(m.accountDir(id), "home")
}

// setupAccountLayout 搭建隔离账号的目录结构并生成软链接与私有文件。
// 严禁修改或创建 HostHome 目录中的任何文件；目标不存在时照常创建软链接；已存在软链接跳过。
func (m *Manager) setupAccountLayout(id string) error {
	home := m.accountHomeDir(id)
	geminiDir := filepath.Join(home, ".gemini")
	antigravityCLIDir := filepath.Join(geminiDir, "antigravity-cli")

	if err := os.MkdirAll(antigravityCLIDir, dirPerm0700); err != nil {
		return fmt.Errorf("create antigravity-cli dir: %w", err)
	}

	// 1. 初始化私有文件 installation_id（若未存在）
	instIDPath := filepath.Join(geminiDir, "installation_id")
	if _, err := os.Stat(instIDPath); os.IsNotExist(err) {
		instID := uuid.NewString()
		if err := os.WriteFile(instIDPath, []byte(instID), filePerm0600); err != nil {
			return fmt.Errorf("write installation_id: %w", err)
		}
	}

	// 2. 软链接配置与映射列表（相对 home 路径 -> 相对 HostHome 路径）
	symlinks := []string{
		filepath.Join(".gemini", "antigravity-cli", "settings.json"),
		filepath.Join(".gemini", "config"),
		filepath.Join(".gemini", "skills"),
		filepath.Join(".gemini", "hooks"),
		filepath.Join(".gemini", "conversations"),
		filepath.Join(".gemini", "brain"),
		filepath.Join(".gemini", "annotations"),
		".gitconfig",
		".ssh",
		".config",
		".npmrc",
		".zshrc",
	}

	for _, rel := range symlinks {
		// .zshrc 是 macOS 专属路径，Windows 上跳过。
		if goos == "windows" && macOnlyLinks[rel] {
			continue
		}

		target := filepath.Join(m.opts.HostHome, rel)
		linkPath := filepath.Join(home, rel)

		// 检查链接本身是否存在（即使为断链）
		if _, err := os.Lstat(linkPath); err == nil {
			continue // 链接已存在，跳过
		}

		if err := os.MkdirAll(filepath.Dir(linkPath), dirPerm0700); err != nil {
			return fmt.Errorf("mkdir parent of %s: %w", linkPath, err)
		}

		// 共享宿主配置只是便利，失败不阻断账号创建；Windows 无权限建软链接时由 LinkOrCopy 退化。
		if err := fsutil.LinkOrCopy(target, linkPath); err != nil {
			log.Printf("agy accounts: 无法共享宿主配置 %s（%v），已跳过", rel, err)
		}
	}

	return m.isolateLibrary(home)
}

// prepareKeychain 是准备账号隔离钥匙串的可注入副本：测试里替换它即可避免触碰真实钥匙串。
var prepareKeychain = prepareAccountKeychain

// prepareAccountKeychain 为隔离账号准备并解锁独立的钥匙串。
// macOS 的 security unlock-keychain 对基名为 login.keychain-db 的文件会校验系统登录密码而非空密码，
// 因此实际文件落为同目录下的 account.keychain-db，再由 login.keychain-db 软链指向它；
// 新建时把 HOME 指向临时空目录，避免 create-keychain 把隔离路径写入宿主 com.apple.security.plist。
func prepareAccountKeychain(path string) error {
	runSecurity := func(env []string, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := procattr.HideWindow(exec.CommandContext(ctx, "/usr/bin/security", args...))
		if env != nil {
			cmd.Env = env
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("security %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm0700); err != nil {
		return fmt.Errorf("mkdir keychain dir: %w", err)
	}

	targetPath := path
	if base := filepath.Base(path); base == "login.keychain-db" || base == "login.keychain" {
		targetPath = filepath.Join(filepath.Dir(path), "account.keychain-db")
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink == 0 {
			if _, terr := os.Stat(targetPath); os.IsNotExist(terr) {
				if rerr := os.Rename(path, targetPath); rerr != nil {
					return fmt.Errorf("rename keychain: %w", rerr)
				}
			}
		}
	}

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		tmpHome, err := os.MkdirTemp("", "mcode-keychain-home-*")
		if err != nil {
			return fmt.Errorf("create temp home: %w", err)
		}
		defer os.RemoveAll(tmpHome)

		environ := os.Environ()
		env := make([]string, 0, len(environ)+1)
		for _, e := range environ {
			if strings.HasPrefix(e, "HOME=") {
				continue
			}
			env = append(env, e)
		}
		env = append(env, "HOME="+tmpHome)

		if err := runSecurity(env, "create-keychain", "-p", "", targetPath); err != nil {
			return err
		}
		if err := runSecurity(nil, "set-keychain-settings", targetPath); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("stat keychain: %w", err)
	}

	if targetPath != path {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if err := os.Symlink(filepath.Base(targetPath), path); err != nil {
				return fmt.Errorf("symlink keychain: %w", err)
			}
		}
	}

	return runSecurity(nil, "unlock-keychain", "-p", "", targetPath)
}

// isolateLibrary 隔离 Library 目录：共享 Keychains 会让 agy 读写宿主凭据，因此排除 Keychains 并在 macOS 上自建隔离钥匙串，避免 agy 写 token 时因找不到默认钥匙串而弹窗。
func (m *Manager) isolateLibrary(home string) error {
	if goos == "windows" {
		return nil
	}

	libPath := filepath.Join(home, "Library")
	if info, err := os.Lstat(libPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(libPath); err != nil {
				return fmt.Errorf("remove Library symlink: %w", err)
			}
		} else if !info.IsDir() {
			return fmt.Errorf("Library exists and is not a directory or symlink: %s", libPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat Library: %w", err)
	}

	if err := os.MkdirAll(libPath, dirPerm0700); err != nil {
		return fmt.Errorf("create Library dir: %w", err)
	}

	keychainsPath := filepath.Join(libPath, "Keychains")
	if info, err := os.Lstat(keychainsPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(keychainsPath); err != nil {
			return fmt.Errorf("remove Keychains symlink: %w", err)
		}
	}

	if goos == "darwin" {
		if err := prepareKeychain(filepath.Join(keychainsPath, "login.keychain-db")); err != nil {
			log.Printf("agy accounts: 无法准备隔离钥匙串（%v），已跳过", err)
		}
	}

	hostLib := filepath.Join(m.opts.HostHome, "Library")
	entries, err := os.ReadDir(hostLib)
	if err != nil {
		log.Printf("agy accounts: 无法读取宿主 Library 目录（%v），已跳过", err)
		return nil
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "Keychains" {
			continue
		}
		linkPath := filepath.Join(libPath, name)
		if _, err := os.Lstat(linkPath); err == nil {
			continue
		}
		target := filepath.Join(hostLib, name)
		if err := os.Symlink(target, linkPath); err != nil {
			log.Printf("agy accounts: 无法共享宿主 Library 条目 %s（%v），已跳过", name, err)
		}
	}

	return nil
}

// generateDeviceProfile 按照规划 §6.1 生成独立的设备指纹。
func generateDeviceProfile() DeviceProfile {
	// machineId: auth0|user_ 加 32 位 hex（取 sha256 随机值的 16 字节即 32 个 hex 字符）
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	h := sha256.Sum256(buf)
	machineHex := hex.EncodeToString(h[:16])
	machineID := "auth0|user_" + machineHex

	macMachineID := uuid.NewString()
	devDeviceID := uuid.NewString()
	sqmID := fmt.Sprintf("{%s}", strings.ToUpper(uuid.NewString()))

	return DeviceProfile{
		MachineId:        machineID,
		MacMachineId:     macMachineID,
		DevDeviceId:      devDeviceID,
		SqmId:            sqmID,
		ServiceMachineId: devDeviceID,
	}
}
