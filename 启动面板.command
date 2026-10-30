#!/bin/bash
# 启动面板.command — macOS 双击入口
#
# 双击后在终端里完成：编译（必要时）→ 启动网关 → 健康检查 → 自动打开面板。
# 关窗或 Ctrl+C 即停止服务。真正的逻辑在 cmd/launcher（与 Windows/Linux 共用）。
cd "$(dirname "$0")" || exit 1
exec ./start.sh "$@"
