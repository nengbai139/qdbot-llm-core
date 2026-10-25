package clientid

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// GetSystemDeviceID 从系统获取设备编码，与 qdbot 一致。
// 优先级：1) Linux /etc/machine-id；2) macOS IOPlatformUUID；3) 主机名；4) ~/.qdbot/device_id。
func GetSystemDeviceID() string {
	if id := readMachineID(); id != "" {
		return id
	}
	if runtime.GOOS == "darwin" {
		if id := readDarwinPlatformUUID(); id != "" {
			return id
		}
	}
	if name, _ := os.Hostname(); name != "" {
		if s := sanitize(name); len(s) >= 1 {
			return s
		}
	}
	return getOrCreatePersistedDeviceID()
}

func readMachineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(b))
		if id != "" && len(id) <= 64 {
			return sanitize(id)
		}
	}
	return ""
}

func readDarwinPlatformUUID() string {
	out, err := exec.Command("ioreg", "-rd2", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([A-Fa-f0-9\-]+)"`)
	matches := re.FindSubmatch(out)
	if len(matches) >= 2 {
		id := strings.TrimSpace(string(matches[1]))
		if id != "" && len(id) <= 64 {
			return sanitize(id)
		}
	}
	return ""
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func getOrCreatePersistedDeviceID() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return generateShortID()
	}
	path := filepath.Join(home, ".qdbot", "device_id")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" && len(id) <= 64 {
			return sanitize(id)
		}
	}
	id := generateShortID()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte(id), 0600)
	return id
}

func generateShortID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "qdbot"
	}
	return hex.EncodeToString(b)
}
