//go:build darwin || linux

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 假工具链：把每次调用记到 CHECK_LOG，行为由环境变量控制。
const fakeGo = `#!/usr/bin/env bash
printf '%s %s\n' "$(basename "$0")" "$*" >> "$CHECK_LOG"
if [[ "${1:-}" == "list" ]]; then
    if [[ "$*" == *"-f "* ]]; then
        printf 'mcode/internal/a 1\nmcode/internal/ui 1\nmcode/scripts 0\nmcode/build.noindex/stage0/seed 1\nmcode/dist/darwin-arm64/mcode.app/Contents/Resources 1\n'
    else
        printf 'mcode/internal/a\nmcode/internal/ui\nmcode/build.noindex/stage0/seed\nmcode/dist/darwin-arm64/mcode.app/Contents/Resources\n'
    fi
    exit 0
fi
if [[ "${1:-}" == "test" ]]; then
    is_compile=0
    out_path=""
    prev=""
    for arg in "$@"; do
        if [[ "$prev" == "-o" ]]; then
            out_path="$arg"
            prev=""
            continue
        fi
        case "$arg" in
        -c) is_compile=1 ;;
        -o) prev="-o" ;;
        esac
    done
    if [[ $is_compile -eq 1 && -n "$out_path" ]]; then
        cat <<'EOF' > "$out_path"
#!/usr/bin/env bash
list_mode=0
run_pattern=""
prev=""
for arg in "$@"; do
    if [[ "$prev" == "-test.run" ]]; then
        run_pattern="$arg"
        prev=""
        continue
    fi
    case "$arg" in
    -test.list)
        list_mode=1
        ;;
    -test.run)
        prev="-test.run"
        ;;
    -test.run=*)
        run_pattern="${arg#-test.run=}"
        ;;
    esac
done
if [[ $list_mode -eq 1 ]]; then
    printf 'TestAlpha\nTestBeta\nTestGamma\nTestDelta\nTestEpsilon\nBenchmarkUI\n'
    exit 0
fi
if [[ -n "$run_pattern" ]]; then
    printf '%s %s\n' "$(basename "$0")" "$*" >> "$CHECK_LOG"
    if [[ -n "${CHECK_UI_FAIL:-}" && "$run_pattern" == *"$CHECK_UI_FAIL"* ]]; then
        echo "FAIL: $CHECK_UI_FAIL"
        exit 1
    fi
fi
exit 0
EOF
        chmod +x "$out_path"
    fi
fi
exit 0
`

const fakeGofmt = `#!/usr/bin/env bash
printf '%s %s\n' "$(basename "$0")" "$*" >> "$CHECK_LOG"
printf '%s' "${CHECK_GOFMT_OUTPUT:-}"
exit 0
`

// fakeGit 必须输出至少一个文件：macOS 的 xargs 遇到空输入时不执行命令，
// 原来的假 git 什么都不输出，gofmt 用例就从未真正调用过假 gofmt。
const fakeGit = `#!/usr/bin/env bash
printf '%s %s\n' "$(basename "$0")" "$*" >> "$CHECK_LOG"
if [[ "${1:-}" == "ls-files" ]]; then
    printf 'a.go\0'
fi
exit 0
`

type checkFixture struct {
	root  string
	log   string
	bin   string
	envvs []string
}

// newCheckFixture 在临时目录里搭一个假仓库根：真实的 check.sh 加假工具链，
// 只验证脚本的编排逻辑，不跑真正的构建和测试。
func newCheckFixture(t *testing.T) *checkFixture {
	t.Helper()
	root := t.TempDir()

	scriptsDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("check.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "check.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	// 依赖规则检查是独立脚本，这里用总返回成功的替身。
	if err := os.WriteFile(filepath.Join(scriptsDir, "check-deps.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"go": fakeGo, "gofmt": fakeGofmt, "git": fakeGit} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return &checkFixture{
		root: root,
		log:  filepath.Join(root, "check.log"),
		bin:  bin,
	}
}

// run 跑一遍 check.sh，返回合并输出、日志行和退出错误。
func (f *checkFixture) run(t *testing.T, extra map[string]string) (string, []string, error) {
	t.Helper()
	args := append([]string{filepath.Join(f.root, "scripts", "check.sh")}, f.envvs...)
	cmd := exec.Command("bash", args...)
	env := append(os.Environ(),
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CHECK_LOG="+f.log,
	)
	for k := range extra {
		env = append(env, k+"="+extra[k])
	}
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	return string(output), f.lines(), err
}

func (f *checkFixture) lines() []string {
	data, _ := os.ReadFile(f.log)
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestCheckScriptExcludesBuildOutput(t *testing.T) {
	f := newCheckFixture(t)
	_, lines, err := f.run(t, nil)
	if err != nil {
		t.Fatalf("check.sh 应当成功退出: %v\n%s", err, strings.Join(lines, "\n"))
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "go vet") || (strings.HasPrefix(line, "go test") && !strings.HasPrefix(line, "go test -c")) {
			if !strings.Contains(line, "mcode/internal/a") {
				t.Fatalf("检查命令缺少有效包: %s", line)
			}
		}
		if (strings.Contains(line, "build.noindex") || strings.Contains(line, "mcode/dist/")) &&
			strings.HasPrefix(line, "go ") && !strings.HasPrefix(line, "go list") {
			t.Fatalf("打包产物包不应参与检查: %s", line)
		}
		if strings.Contains(line, "-race") {
			t.Fatalf("未传 --race 时不应跑 race: %s", line)
		}
	}

	var sawBuild, sawVet, sawTest bool
	listCalls := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "go list ") {
			listCalls++
		}
		if strings.HasPrefix(line, "go build") && strings.Contains(line, "mcode/internal/a") {
			if strings.Contains(line, "mcode/scripts") {
				t.Fatalf("仅含测试文件的包不应传入 go build: %s", line)
			}
			sawBuild = true
		}
		if strings.HasPrefix(line, "go vet") && strings.Contains(line, "mcode/internal/a") && strings.Contains(line, "mcode/scripts") {
			sawVet = true
		}
		if strings.HasPrefix(line, "go test") && strings.Contains(line, "mcode/internal/a") && strings.Contains(line, "mcode/scripts") {
			sawTest = true
		}
	}
	if listCalls != 1 {
		t.Fatalf("预期仅调用 1 次 go list，实际 %d:\n%s", listCalls, strings.Join(lines, "\n"))
	}
	if !sawBuild || !sawVet || !sawTest {
		t.Fatalf("缺少 go build、go vet 或 go test 调用:\n%s", strings.Join(lines, "\n"))
	}
}

func TestCheckScriptRaceFlag(t *testing.T) {
	f := newCheckFixture(t)
	f.envvs = []string{"--race"}
	_, lines, err := f.run(t, nil)
	if err != nil {
		t.Fatalf("check.sh --race 应当成功退出: %v\n%s", err, strings.Join(lines, "\n"))
	}

	for _, line := range lines {
		if strings.Contains(line, "test") && strings.Contains(line, "-race") {
			return
		}
	}
	t.Fatalf("缺少带 -race 的 go test 调用:\n%s", strings.Join(lines, "\n"))
}

func TestCheckScriptStopsOnGofmtDiff(t *testing.T) {
	f := newCheckFixture(t)
	output, lines, err := f.run(t, map[string]string{"CHECK_GOFMT_OUTPUT": "bad.go\n"})
	if err == nil {
		t.Fatal("gofmt 有差异时应当非零退出")
	}

	if !strings.Contains(output, "bad.go") {
		t.Fatalf("未报告未格式化文件:\n%s", output)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "go build") {
			t.Fatalf("gofmt 失败后不应继续构建: %s", line)
		}
	}
}

func TestCheckScriptRunsMygoVet(t *testing.T) {
	f := newCheckFixture(t)
	output, lines, err := f.run(t, nil)
	if err != nil {
		t.Fatalf("check.sh 应当成功退出: %v\n%s", err, strings.Join(lines, "\n"))
	}

	if !strings.Contains(output, "==> mygo vet") {
		t.Fatalf("缺少 mygo vet 步骤输出:\n%s", output)
	}
	for _, line := range lines {
		if line == "go run github.com/egoist/mygo/cmd/mygo vet ." {
			return
		}
	}
	t.Fatalf("缺少 mygo vet 调用:\n%s", strings.Join(lines, "\n"))
}

func TestCheckScriptGoTestTimeout(t *testing.T) {
	data, err := os.ReadFile("check.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`go test -timeout 300s "${test_pkgs[@]}"`, `-test.timeout 300s`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("check.sh 缺少预期超时配置 %q", want)
		}
	}
}

func TestCheckScriptShardsUITests(t *testing.T) {
	f := newCheckFixture(t)
	output, lines, err := f.run(t, map[string]string{"MCODE_UI_SHARDS": "4"})
	if err != nil {
		t.Fatalf("check.sh 应当成功退出: %v\n%s\n%s", err, output, strings.Join(lines, "\n"))
	}

	var goTestLines []string
	var shardPatterns []string
	for _, line := range lines {
		if strings.HasPrefix(line, "go test ") && !strings.HasPrefix(line, "go test -c ") {
			goTestLines = append(goTestLines, line)
		}
		if strings.HasPrefix(line, "ui.test ") {
			idx := strings.Index(line, "-test.run ")
			if idx == -1 {
				t.Fatalf("ui.test 调用缺少 -test.run: %s", line)
			}
			shardPatterns = append(shardPatterns, strings.TrimSpace(line[idx+len("-test.run "):]))
		}
	}

	if len(goTestLines) != 1 {
		t.Fatalf("预期恰好 1 条其他包的 go test 调用，实际 %d: %v", len(goTestLines), goTestLines)
	}
	if !strings.Contains(goTestLines[0], "mcode/internal/a") || strings.Contains(goTestLines[0], "mcode/internal/ui") {
		t.Fatalf("其他包的 go test 应包含 mcode/internal/a 且不包含 mcode/internal/ui: %s", goTestLines[0])
	}

	if len(shardPatterns) != 4 {
		t.Fatalf("预期 4 个分片调用，实际 %d: %v", len(shardPatterns), shardPatterns)
	}

	counts := map[string]int{}
	for _, pat := range shardPatterns {
		if !strings.HasPrefix(pat, "^(") || !strings.HasSuffix(pat, ")$") {
			t.Fatalf("分片 -test.run 格式异常: %s", pat)
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(pat, "^("), ")$")
		for _, name := range strings.Split(inner, "|") {
			counts[name]++
		}
	}

	wantTests := []string{"TestAlpha", "TestBeta", "TestGamma", "TestDelta", "TestEpsilon"}
	for _, name := range wantTests {
		if counts[name] != 1 {
			t.Fatalf("测试 %s 应恰好出现在 1 个分片中，实际 %d 次 (patterns=%v)", name, counts[name], shardPatterns)
		}
	}
	if counts["BenchmarkUI"] != 0 {
		t.Fatalf("BenchmarkUI 不应出现在任何分片中 (patterns=%v)", shardPatterns)
	}

	fFail := newCheckFixture(t)
	failOut, _, failErr := fFail.run(t, map[string]string{"MCODE_UI_SHARDS": "4", "CHECK_UI_FAIL": "TestBeta"})
	if failErr == nil {
		t.Fatalf("设置 CHECK_UI_FAIL 时 check.sh 应当非零退出:\n%s", failOut)
	}
	if !strings.Contains(failOut, "FAIL: TestBeta") {
		t.Fatalf("失败时应打印失败分片日志:\n%s", failOut)
	}
}

func TestCheckScriptRaceSkipsNonRaceAndShardsUI(t *testing.T) {
	f := newCheckFixture(t)
	f.envvs = []string{"--race"}
	output, lines, err := f.run(t, map[string]string{"MCODE_UI_SHARDS": "4"})
	if err != nil {
		t.Fatalf("check.sh --race 应当成功退出: %v\n%s\n%s", err, output, strings.Join(lines, "\n"))
	}

	var goTestLines []string
	var goCompileLines []string
	var shardPatterns []string
	for _, line := range lines {
		if strings.HasPrefix(line, "go test ") {
			if strings.Contains(line, " -c ") {
				goCompileLines = append(goCompileLines, line)
			} else {
				goTestLines = append(goTestLines, line)
			}
		}
		if strings.HasPrefix(line, "ui.test ") {
			idx := strings.Index(line, "-test.run ")
			if idx == -1 {
				t.Fatalf("ui.test 调用缺少 -test.run: %s", line)
			}
			shardPatterns = append(shardPatterns, strings.TrimSpace(line[idx+len("-test.run "):]))
		}
	}

	if len(goTestLines) != 1 {
		t.Fatalf("--race 模式预期只跑 1 轮非 UI 包 go test，实际 %d: %v", len(goTestLines), goTestLines)
	}
	if !strings.Contains(goTestLines[0], "-race") || !strings.Contains(goTestLines[0], "mcode/internal/a") || strings.Contains(goTestLines[0], "mcode/internal/ui") {
		t.Fatalf("--race 模式下非 UI 包 go test 参数异常: %s", goTestLines[0])
	}
	if len(goCompileLines) != 1 || !strings.Contains(goCompileLines[0], "-race") || !strings.Contains(goCompileLines[0], "./internal/ui") {
		t.Fatalf("--race 模式下 UI 编译命令异常: %v", goCompileLines)
	}
	if len(shardPatterns) != 4 {
		t.Fatalf("--race 模式下预期 4 个 UI 分片，实际 %d: %v", len(shardPatterns), shardPatterns)
	}
	if !strings.Contains(output, "ok  \tmcode/internal/ui (4 shards, 5 tests)") {
		t.Fatalf("输出缺少 UI 分片汇总行:\n%s", output)
	}
}
