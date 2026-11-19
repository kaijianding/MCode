#!/usr/bin/env bash
set -euo pipefail

# 统一检查入口：格式、构建、go vet、依赖规则、单测，可选 race。
# 兼容 macOS 自带 bash 3.2（不用 mapfile / readarray / 关联数组）。
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RACE=0
for arg in "$@"; do
    case "$arg" in
    --race)
        RACE=1
        ;;
    *)
        echo "用法: $0 [--race]" >&2
        exit 2
        ;;
    esac
done

# 解析 internal/ui 分片数：显式指定 MCODE_UI_SHARDS 时校验正整数，否则按 ncpu/2 并在 [4, 6] 间钳制（避免与非 UI 测试、Web 检查三路并发时过度超卖 CPU）。
resolve_ui_shards() {
    if [[ -n "${MCODE_UI_SHARDS:-}" ]]; then
        if ! [[ "$MCODE_UI_SHARDS" =~ ^[1-9][0-9]*$ ]]; then
            echo "错误: MCODE_UI_SHARDS 必须为正整数: $MCODE_UI_SHARDS" >&2
            exit 1
        fi
        printf '%s' "$MCODE_UI_SHARDS"
        return 0
    fi
    local cpu_count=""
    if command -v sysctl >/dev/null 2>&1; then
        cpu_count="$(sysctl -n hw.ncpu 2>/dev/null || true)"
    fi
    if [[ -z "$cpu_count" ]] && command -v nproc >/dev/null 2>&1; then
        cpu_count="$(nproc 2>/dev/null || true)"
    fi
    if ! [[ "$cpu_count" =~ ^[1-9][0-9]*$ ]]; then
        cpu_count=8
    fi
    local shards=$(( cpu_count / 2 ))
    if (( shards < 4 )); then
        shards=4
    elif (( shards > 6 )); then
        shards=6
    fi
    printf '%s' "$shards"
}

UI_SHARD_COUNT="$(resolve_ui_shards)"

echo "==> 收集待检查包"
# build.noindex 是被 git 忽略的打包产物目录，应用包的 Contents/Resources 里带 .go 文件，
# ./... 会把它们扫进来，这里过滤掉；发布打包产物目录 dist/ 同样被 git 忽略、里面也带 .go 文件，
# 所以一并排除。
# 像 mcode/scripts 这样只有测试文件的包，显式交给 go build 会报 no non-test Go files 并失败，
# 所以用单次 go list 同时收集全部待检查包 pkgs 与含非测试 Go 文件的 build_pkgs。
pkgs=()
build_pkgs=()
while IFS=' ' read -r pkg has_go; do
    if [[ -z "$pkg" ]]; then
        continue
    fi
    pkgs+=("$pkg")
    if [[ "${has_go:-1}" != "0" ]]; then
        build_pkgs+=("$pkg")
    fi
done < <(CGO_ENABLED=0 go list -f '{{.ImportPath}} {{if .GoFiles}}1{{else}}0{{end}}' ./... | grep -vE '^mcode/(build\.noindex|dist)/' || true)
if [[ ${#pkgs[@]} -eq 0 ]]; then
    echo "没有可检查的包" >&2
    exit 1
fi
if [[ ${#build_pkgs[@]} -eq 0 ]]; then
    echo "没有可构建的包" >&2
    exit 1
fi

echo "==> gofmt 检查"
unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"
if [[ -n "$unformatted" ]]; then
    echo "以下文件未格式化，请运行 gofmt -w:" >&2
    echo "$unformatted" >&2
    exit 1
fi

echo "==> go build"
CGO_ENABLED=0 go build "${build_pkgs[@]}"

echo "==> go vet"
CGO_ENABLED=0 go vet "${pkgs[@]}"

echo "==> mygo vet"
go run github.com/egoist/mygo/cmd/mygo vet .

echo "==> 依赖规则检查"
bash scripts/check-deps.sh

echo "==> go test & 前端检查"
test_pkgs=()
has_ui=0
for pkg in "${pkgs[@]}"; do
    if [[ "$pkg" == "mcode/internal/ui" ]]; then
        has_ui=1
    else
        test_pkgs+=("$pkg")
    fi
done

run_tmp="$(mktemp -d)"
trap 'rm -rf "${run_tmp:-}"' EXIT

test_pkgs_pid=""
test_pkgs_log="$run_tmp/test-pkgs.log"
if [[ ${#test_pkgs[@]} -gt 0 ]]; then
    (
        if [[ $RACE -eq 1 ]]; then
            echo "==> go test -race (非 UI 包)"
            # race 检测需要 cgo，前面已用 CGO_ENABLED=0 校验过无 cgo 依赖，这里直接跑 race 一轮。
            env -u CGO_ENABLED go test -race -timeout 300s "${test_pkgs[@]}"
        else
            echo "==> go test (非 UI 包)"
            CGO_ENABLED=0 go test -timeout 300s "${test_pkgs[@]}"
        fi
    ) >"$test_pkgs_log" 2>&1 &
    test_pkgs_pid=$!
fi

ui_lane_pid=""
ui_lane_log="$run_tmp/ui-lane.log"
if [[ $has_ui -eq 1 ]]; then
    (
        if [[ $RACE -eq 1 ]]; then
            echo "==> go test -race -c ./internal/ui"
            env -u CGO_ENABLED go test -race -c -o "$run_tmp/ui.test" ./internal/ui
        else
            echo "==> go test -c ./internal/ui"
            CGO_ENABLED=0 go test -c -o "$run_tmp/ui.test" ./internal/ui
        fi

        ui_list="$("$run_tmp/ui.test" -test.list '.')"
        ui_shards=()
        for (( i = 0; i < UI_SHARD_COUNT; i++ )); do
            ui_shards+=("")
        done
        ui_idx=0
        while IFS= read -r name; do
            if [[ -z "$name" ]]; then
                continue
            fi
            shard=$((ui_idx % UI_SHARD_COUNT))
            if [[ -z "${ui_shards[$shard]}" ]]; then
                ui_shards[$shard]="$name"
            else
                ui_shards[$shard]="${ui_shards[$shard]}|$name"
            fi
            ui_idx=$((ui_idx + 1))
        done < <(printf '%s\n' "$ui_list" | grep -E '^(Test|Example|Fuzz)' || true)

        ui_pids=()
        ui_logs=()
        for (( i = 0; i < UI_SHARD_COUNT; i++ )); do
            if [[ -z "${ui_shards[$i]}" ]]; then
                continue
            fi
            log_file="$run_tmp/ui-shard-$i.log"
            ui_logs[$i]="$log_file"
            (
                cd internal/ui
                exec "$run_tmp/ui.test" -test.timeout 300s -test.run "^(${ui_shards[$i]})$"
            ) >"$log_file" 2>&1 &
            ui_pids[$i]=$!
        done

        ui_failed=0
        for (( i = 0; i < UI_SHARD_COUNT; i++ )); do
            if [[ -z "${ui_pids[$i]:-}" ]]; then
                continue
            fi
            if ! wait "${ui_pids[$i]}"; then
                ui_failed=1
                echo "==> internal/ui 分片 $((i + 1)) 失败:" >&2
                cat "${ui_logs[$i]}" >&2
            fi
        done
        if [[ $ui_failed -ne 0 ]]; then
            exit 1
        fi
        echo "ok  	mcode/internal/ui (${UI_SHARD_COUNT} shards, $ui_idx tests)"
    ) >"$ui_lane_log" 2>&1 &
    ui_lane_pid=$!
fi

web_lane_pid=""
web_lane_log="$run_tmp/web-lane.log"
(
    echo "==> 前端类型检查"
    if [[ -d web/node_modules ]]; then
        (cd web && npx tsc --noEmit)
    else
        echo "web/node_modules 不存在，跳过前端类型检查"
    fi

    echo "==> 前端测试"
    if [[ -d web/node_modules ]]; then
        (cd web && npm test --silent)
    else
        echo "web/node_modules 不存在，跳过前端测试"
    fi
) >"$web_lane_log" 2>&1 &
web_lane_pid=$!

failed=0
if [[ -n "$test_pkgs_pid" ]]; then
    if wait "$test_pkgs_pid"; then
        cat "$test_pkgs_log"
    else
        failed=1
        echo "==> 非 UI 包测试失败:" >&2
        cat "$test_pkgs_log" >&2
    fi
fi
if [[ -n "$ui_lane_pid" ]]; then
    if wait "$ui_lane_pid"; then
        cat "$ui_lane_log"
    else
        failed=1
        cat "$ui_lane_log" >&2
    fi
fi
if [[ -n "$web_lane_pid" ]]; then
    if wait "$web_lane_pid"; then
        cat "$web_lane_log"
    else
        failed=1
        echo "==> 前端检查失败:" >&2
        cat "$web_lane_log" >&2
    fi
fi
if [[ $failed -ne 0 ]]; then
    exit 1
fi

echo "全部检查通过"
