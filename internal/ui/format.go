package mcodeui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// formatTokenCount 将 Token 数量格式化为紧凑字符串（如 '76k', '1.2M', '950'）。
func formatTokenCount(n int64) string {
	if n < 0 {
		return "-" + formatTokenCount(-n)
	}
	compact := func(val float64, suffix string) string {
		s := fmt.Sprintf("%.1f", val)
		s = strings.TrimSuffix(s, ".0")
		return s + suffix
	}
	if n >= 1_000_000_000 {
		return compact(float64(n)/1_000_000_000.0, "B")
	}
	if n >= 1_000_000 {
		return compact(float64(n)/1_000_000.0, "M")
	}
	if n >= 1_000 {
		return compact(float64(n)/1_000.0, "k")
	}
	return strconv.FormatInt(n, 10)
}

// formatFileSize 将字节数格式化为易读的文件大小字符串。
func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(bytes) / 1024.0
	for _, u := range units {
		if v < 1024.0 || u == "TB" {
			if v >= 100 || v == float64(int(v)) {
				return fmt.Sprintf("%.0f %s", v, u)
			}
			return fmt.Sprintf("%.1f %s", v, u)
		}
		v /= 1024.0
	}
	return fmt.Sprintf("%d B", bytes)
}

// formatDuration 将时长格式化为统一紧凑文本：
// d <= 0 返回 "0s"，不足 1 分钟返回 "5s"，不足 1 小时返回 "1m 05s"，其余返回 "1h 01m"。
// 秒与分钟固定补两位，避免实时计时时宽度跳动。
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	default:
		return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
}
