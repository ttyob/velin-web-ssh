#!/bin/sh
set -eu

REPOSITORY="ttyob/velin-web-ssh"
IMAGE_REPOSITORY="ghcr.io/ttyob/velin-web-ssh"
VERSION="${VELIN_VERSION:-latest}"

if [ "$(uname -s)" != "Linux" ]; then
  echo "VelinWebSSH 快速安装目前仅支持 Linux。" >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  INSTALL_DIR="${VELIN_INSTALL_DIR:-/opt/velin}"
else
  INSTALL_DIR="${VELIN_INSTALL_DIR:-${HOME}/.local/share/velin}"
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "未检测到 Docker，请先安装 Docker Engine。" >&2
  echo "https://docs.docker.com/engine/install/" >&2
  exit 1
fi

DOCKER="docker"
if ! docker info >/dev/null 2>&1; then
  if command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
    DOCKER="sudo docker"
  else
    echo "当前用户无权访问 Docker，请加入 docker 用户组或使用 sudo 运行安装脚本。" >&2
    exit 1
  fi
fi

if ! $DOCKER compose version >/dev/null 2>&1; then
  echo "未检测到 Docker Compose v2 插件。" >&2
  exit 1
fi

if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -qO "$2" "$1"; }
else
  echo "需要 curl 或 wget 下载发布配置。" >&2
  exit 1
fi

generate_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 16
  else
    od -An -N16 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

SOURCE_REF="main"
IMAGE_TAG="latest"
if [ "$VERSION" != "latest" ]; then
  SOURCE_REF="$VERSION"
  IMAGE_TAG="${VERSION#v}"
fi

mkdir -p "$INSTALL_DIR/data"
COMPOSE_TMP="$(mktemp)"
UPDATE_TMP="$(mktemp)"
trap 'rm -f "$COMPOSE_TMP" "$UPDATE_TMP"' EXIT INT TERM
download "https://raw.githubusercontent.com/${REPOSITORY}/${SOURCE_REF}/compose.release.yaml" "$COMPOSE_TMP"
download "https://raw.githubusercontent.com/${REPOSITORY}/${SOURCE_REF}/update.sh" "$UPDATE_TMP"
mv "$COMPOSE_TMP" "$INSTALL_DIR/compose.yaml"
mv "$UPDATE_TMP" "$INSTALL_DIR/update.sh"
chmod 755 "$INSTALL_DIR/update.sh"

PASSWORD_CREATED=false
if [ ! -f "$INSTALL_DIR/.env" ]; then
  ADMIN_PASSWORD="${VELIN_ADMIN_PASSWORD:-$(generate_password)}"
  umask 077
  {
    echo "VELIN_IMAGE=${IMAGE_REPOSITORY}:${IMAGE_TAG}"
    echo "VELIN_ADMIN_USER=${VELIN_ADMIN_USER:-admin}"
    echo "VELIN_ADMIN_PASSWORD=${ADMIN_PASSWORD}"
    echo "VELIN_ADDR=${VELIN_ADDR:-0.0.0.0:8377}"
    echo "VELIN_COOKIE_SECURE=${VELIN_COOKIE_SECURE:-true}"
    echo "VELIN_EMBED_ORIGINS=${VELIN_EMBED_ORIGINS:-}"
    echo "VELIN_TRUSTED_PROXY_CIDRS=${VELIN_TRUSTED_PROXY_CIDRS:-}"
    echo "VELIN_HOST_PORT_ADDR=${VELIN_HOST_PORT_ADDR:-127.0.0.1}"
  } > "$INSTALL_DIR/.env"
  chmod 600 "$INSTALL_DIR/.env"
  PASSWORD_CREATED=true
else
  ADMIN_PASSWORD=""
  if grep -q '^VELIN_IMAGE=ghcr.io/ttyob/velinwebssh:' "$INSTALL_DIR/.env"; then
    sed -i 's#^VELIN_IMAGE=ghcr.io/ttyob/velinwebssh:#VELIN_IMAGE=ghcr.io/ttyob/velin-web-ssh:#' "$INSTALL_DIR/.env"
  fi
fi

cd "$INSTALL_DIR"
$DOCKER compose pull
$DOCKER compose run --rm --no-deps --user root --entrypoint sh velin \
  -c 'chown -R velin:velin /app/data && chmod 700 /app/data'
$DOCKER compose up -d

attempt=0
until $DOCKER compose exec -T velin sh -c \
  'port=${VELIN_ADDR##*:}; wget -q -O - "http://127.0.0.1:${port}/api/health/ready"' >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "VelinWebSSH 未能在预期时间内启动，请检查：cd $INSTALL_DIR && $DOCKER compose logs" >&2
    exit 1
  fi
  sleep 2
done

SERVICE_ADDR="$($DOCKER compose exec -T velin sh -c 'printf "%s" "$VELIN_ADDR"')"
SERVICE_PORT="${SERVICE_ADDR##*:}"
HOST_IP="127.0.0.1"
case "$SERVICE_ADDR" in
127.0.0.1:*|localhost:*) ;;
*) if command -v hostname >/dev/null 2>&1; then
  detected="$(hostname -I 2>/dev/null | awk '{print $1}')"
  [ -n "$detected" ] && HOST_IP="$detected"
fi ;;
esac

echo
echo "VelinWebSSH 已安装完成"
echo "访问地址: http://${HOST_IP}:${SERVICE_PORT}"
echo "安装目录: ${INSTALL_DIR}"
if [ "$PASSWORD_CREATED" = true ]; then
  echo "管理员用户: ${VELIN_ADMIN_USER:-admin}"
  echo "初始密码: ${ADMIN_PASSWORD}"
  echo "请登录后立即修改密码。"
else
  echo "已保留现有 .env 和管理员配置。"
fi
echo "升级命令: ${INSTALL_DIR}/update.sh"
