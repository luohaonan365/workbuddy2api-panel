#!/usr/bin/env bash
# start.sh — WorkBuddy2API 启动器入口（macOS / Linux）
#
# 用法:
#   ./start.sh                      # 在当前目录启动（默认：脚本所在目录）
#   ./start.sh /path/to/runtime     # 指定运行目录（config.json / auths / data / bin 所在处）
#
# 职责边界：本脚本只做「找 Go → 编译启动器 → 交给启动器」，其余逻辑（配置与账号自检、
# 端口占用处理、按需编译网关、健康检查、打开面板、信号处理）都在 cmd/launcher 里，
# 三个平台共用同一份实现。
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
cd "$SCRIPT_DIR" || exit 1

TARGET="${1:-${WB2API_HOME:-$SCRIPT_DIR}}"
if [[ $# -gt 0 ]]; then shift; fi

pause_if_tty() {
    if [[ -t 0 ]]; then
        printf '按回车键关闭窗口...'
        read -r
    fi
}

find_go() {
    if [[ -n "${WB2API_GO:-}" && -x "${WB2API_GO}" ]]; then
        printf '%s' "$WB2API_GO"
        return 0
    fi
    if command -v go >/dev/null 2>&1; then
        command -v go
        return 0
    fi
    local c
    for c in "$HOME/goroot/bin/go" "$HOME/go/bin/go" \
             /usr/local/go/bin/go /opt/homebrew/bin/go /usr/local/bin/go /snap/bin/go \
             "$HOME"/sdk/go*/bin/go; do
        if [[ -x "$c" ]]; then
            printf '%s' "$c"
            return 0
        fi
    done
    return 1
}

GO_BIN="$(find_go)" || {
    echo "未检测到 Go 工具链（编译启动器需要 Go 1.22+）。"
    echo "  macOS:  brew install go"
    echo "  Linux:  用发行版包管理器安装 golang，或从 https://go.dev/dl/ 下载"
    echo "  已安装但不在 PATH: 设置 WB2API_GO=/path/to/go 后重试"
    pause_if_tty
    exit 1
}

mkdir -p "$SCRIPT_DIR/bin"
LAUNCHER="$SCRIPT_DIR/bin/wb2api-launcher"

need_build=0
if [[ ! -x "$LAUNCHER" ]]; then
    need_build=1
elif [[ -n "$(find "$SCRIPT_DIR/cmd/launcher" -name '*.go' -newer "$LAUNCHER" 2>/dev/null | head -1)" ]]; then
    need_build=1
fi

if [[ "$need_build" -eq 1 ]]; then
    echo "正在编译启动器..."
    if ! CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags "-s -w" -o "$LAUNCHER" ./cmd/launcher; then
        echo "启动器编译失败，请把上方报错发出来排查。"
        pause_if_tty
        exit 1
    fi
fi

exec "$LAUNCHER" -dir "$TARGET" "$@"
