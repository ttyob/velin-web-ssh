#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
cd "$SCRIPT_DIR"

if ! command -v npm >/dev/null 2>&1; then
  echo "未找到 npm，无法构建前端。" >&2
  exit 1
fi

npm --prefix web run build

CONTAINER="$(docker compose ps -q velin)"
if [ -z "$CONTAINER" ]; then
  echo "VelinWebSSH 容器未运行，请先执行 docker compose up -d。" >&2
  exit 1
fi

docker cp web/dist/. "$CONTAINER:/app/web/dist/"
echo "前端已更新，未重启 VelinWebSSH 容器，会话保持不变。"
