package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"mcode/internal/fsutil"
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

// isolateLibrary 隔离 Library 目录：共享 Keychains 会让 agy 读宿主钥匙串里的凭据，而不是隔离目录的 token 文件。
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
