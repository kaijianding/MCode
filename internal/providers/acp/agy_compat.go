package acp

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"mcode/internal/fsutil"

	_ "modernc.org/sqlite"
)

var agySessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func agyCommand(input []byte) string {
	var values map[string]any
	if jsonErr := json.Unmarshal(input, &values); jsonErr != nil {
		return ""
	}
	for _, key := range []string{"command", "CommandLine", "command_line", "commandLine"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func agyReadPath(title string, locations []struct {
	Path string `json:"path"`
}, raw []byte) string {
	if title != "Running client_view_file" && title != "Run client_view_file?" {
		return ""
	}
	if len(locations) > 0 && locations[0].Path != "" {
		return locations[0].Path
	}
	var input struct {
		AbsolutePath string `json:"absolute_path"`
	}
	if jsonErr := json.Unmarshal(raw, &input); jsonErr == nil {
		return input.AbsolutePath
	}
	return ""
}

func agyGeminiDir(args, env []string) string {
	for i, arg := range args {
		if arg == "--gemini_dir" && i+1 < len(args) && args[i+1] != "" {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--gemini_dir=") {
			if value := strings.TrimPrefix(arg, "--gemini_dir="); value != "" {
				return value
			}
		}
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "ANTIGRAVITY_GEMINI_DIR=") {
			if value := strings.TrimPrefix(entry, "ANTIGRAVITY_GEMINI_DIR="); value != "" {
				return value
			}
		}
	}
	home := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			home = strings.TrimPrefix(entry, "HOME=")
			break
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".gemini")
}

// agySessionDB 返回 agy 会话库路径。agy 1.5.x 起写入 antigravity-cli，
// 旧版本写在 antigravity-acp，优先返回实际存在的那个。
func agySessionDB(geminiDir, sid string) string {
	if !agySessionIDPattern.MatchString(sid) {
		return ""
	}
	cliDB := filepath.Join(geminiDir, "antigravity-cli", "conversations", sid+".db")
	acpDB := filepath.Join(geminiDir, "antigravity-acp", "conversations", sid+".db")
	for _, dbPath := range []string{cliDB, acpDB} {
		if _, err := os.Stat(dbPath); err == nil {
			return dbPath
		}
	}
	return cliDB
}

func agyOpenSteps(dbPath string) (*sql.DB, error) {
	u := fsutil.FileURL(dbPath)
	u.RawQuery = "mode=ro"
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("PRAGMA busy_timeout = 100"); err == nil {
		var exists int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='steps'").Scan(&exists)
		if err == nil && exists == 0 {
			err = fmt.Errorf("steps 表不存在")
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func agyStepIndex(dbPath string) (int64, bool) {
	if dbPath == "" {
		return -1, false
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return -1, true
	} else if err != nil {
		return -1, false
	}
	db, err := agyOpenSteps(dbPath)
	if err != nil {
		return -1, false
	}
	defer db.Close()
	var index sql.NullInt64
	if err = db.QueryRow("SELECT max(idx) FROM steps").Scan(&index); err != nil {
		return -1, false
	}
	if !index.Valid {
		return -1, true
	}
	return index.Int64, true
}

func agyStepsUsage(dbPath string, start int64) acpPromptUsage {
	var usage acpPromptUsage
	if dbPath == "" {
		return usage
	}
	db, err := agyOpenSteps(dbPath)
	if err != nil {
		return usage
	}
	defer db.Close()
	rows, err := db.Query("SELECT metadata FROM steps WHERE idx > ?", start)
	if err != nil {
		return usage
	}
	defer rows.Close()
	for rows.Next() {
		var metadata []byte
		if err := rows.Scan(&metadata); err != nil {
			return acpPromptUsage{}
		}
		step := agyStepUsage(metadata)
		usage.InputTokens += step.InputTokens
		usage.OutputTokens += step.OutputTokens
		usage.CachedReadTokens += step.CachedReadTokens
		usage.ThoughtTokens += step.ThoughtTokens
	}
	if rows.Err() != nil {
		return acpPromptUsage{}
	}
	return usage
}

func agyStepUsage(metadata []byte) acpPromptUsage {
	var usage acpPromptUsage
	valid := true
	if !walkAgyProto(metadata, func(field, wire, _ uint64, payload []byte) {
		if field != 9 || wire != 2 {
			return
		}
		if !walkAgyProto(payload, func(field, wire, value uint64, _ []byte) {
			if wire != 0 || int64(value) < 0 {
				return
			}
			switch field {
			case 2:
				usage.InputTokens = int64(value)
			case 3:
				usage.OutputTokens = int64(value)
			case 5:
				usage.CachedReadTokens = int64(value)
			case 9:
				usage.ThoughtTokens = int64(value)
			}
		}) {
			valid = false
		}
	}) || !valid {
		return acpPromptUsage{}
	}
	return usage
}

func walkAgyProto(data []byte, visit func(field, wire, value uint64, payload []byte)) bool {
	var groups []uint64
	for len(data) > 0 {
		tag, tagSize := binary.Uvarint(data)
		field, wire := tag>>3, tag&7
		if tagSize <= 0 || field == 0 || field >= 1<<29 {
			return false
		}
		data = data[tagSize:]
		var value, size uint64
		switch wire {
		case 0, 2:
			number, numberSize := binary.Uvarint(data)
			if numberSize <= 0 {
				return false
			}
			data = data[numberSize:]
			if wire == 0 {
				value = number
			} else {
				size = number
			}
		case 1:
			size = 8
		case 3:
			groups = append(groups, field)
			continue
		case 4:
			if len(groups) == 0 || groups[len(groups)-1] != field {
				return false
			}
			groups = groups[:len(groups)-1]
			continue
		case 5:
			size = 4
		default:
			return false
		}
		if size > uint64(len(data)) {
			return false
		}
		if len(groups) == 0 {
			visit(field, wire, value, data[:size])
		}
		data = data[size:]
	}
	return len(groups) == 0
}

func agyNewStepError(dbPath string, start int64) (string, bool) {
	if dbPath == "" {
		return "", false
	}
	db, err := agyOpenSteps(dbPath)
	if err != nil {
		return "", false
	}
	defer db.Close()
	var kind int
	var payload []byte
	err = db.QueryRow("SELECT step_type, substr(step_payload,1,16384) FROM steps WHERE idx > ? ORDER BY idx DESC LIMIT 1", start).Scan(&kind, &payload)
	if err != nil || kind != 17 {
		return "", false
	}
	var parts []string
	segment := make([]rune, 0, 64)
	appendSegment := func() {
		part := strings.TrimSpace(string(segment))
		if utf8.RuneCountInString(part) >= 16 {
			parts = append(parts, part)
		}
		segment = segment[:0]
	}
	for remaining := payload; len(remaining) > 0; {
		r, size := utf8.DecodeRune(remaining)
		remaining = remaining[size:]
		if (r == utf8.RuneError && size == 1) || (!unicode.IsPrint(r) && r != ' ') {
			appendSegment()
			continue
		}
		segment = append(segment, r)
	}
	appendSegment()
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text == "" {
		return "", false
	}
	if runes := []rune(text); len(runes) > 4096 {
		text = string(runes[:4096])
	}
	return text, true
}

func agyTurnFailure(dbPath string, start int64, stopReason string) error {
	if text, ok := agyNewStepError(dbPath, start); ok {
		return fmt.Errorf("agy turn failed: %s", text)
	}
	if stopReason == "refusal" {
		return fmt.Errorf("agy turn failed: agy quota exhausted (stopReason refusal)")
	}
	return nil
}
