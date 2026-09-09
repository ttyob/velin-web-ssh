#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
VERSION="${VELIN_FNOS_VERSION:-0.3.45}"
VERSION="${VERSION#v}"
ARCH="${VELIN_FNOS_ARCH:-$(uname -m)}"
GUACD_IMAGE="${VELIN_FNOS_GUACD_IMAGE:-guacamole/guacd:1.6.0}"
OUTPUT_DIR="${VELIN_FNOS_OUTPUT_DIR:-$REPO_DIR/dist/fnos}"
PREBUILT_BINARY="${VELIN_FNOS_BINARY:-}"
FNPACK_BIN="${FNPACK_BIN:-}"
TMP_DIR=""
STAGE_DIR=""
CONTAINER_ID=""

case "$ARCH" in
  x86_64|amd64)
    ARCH=amd64
    GOARCH=amd64
    FNOS_PLATFORM=x86
    ;;
  aarch64|arm64)
    ARCH=arm64
    GOARCH=arm64
    FNOS_PLATFORM=arm
    ;;
  *)
    echo "Unsupported fnOS architecture: $ARCH" >&2
    exit 1
    ;;
esac

cleanup() {
  if [ -n "$CONTAINER_ID" ]; then
    docker rm "$CONTAINER_ID" >/dev/null 2>&1 || true
  fi
  if [ -n "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
  if [ -n "$STAGE_DIR" ]; then
    rm -rf "$STAGE_DIR"
  fi
}
trap cleanup EXIT INT TERM

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command is missing: $1" >&2
    exit 1
  fi
}

require_command docker
require_command tar

for ICON_FILE in \
  "$SCRIPT_DIR/ICON.PNG" \
  "$SCRIPT_DIR/ICON_256.PNG" \
  "$SCRIPT_DIR/app/ui/images/icon_64.png" \
  "$SCRIPT_DIR/app/ui/images/icon_256.png"
do
  if [ ! -s "$ICON_FILE" ]; then
    echo "Required fnOS icon is missing or empty: $ICON_FILE" >&2
    exit 1
  fi
  if [ "$(wc -c < "$ICON_FILE")" -gt 1048576 ]; then
    echo "fnOS icon exceeds the 1024 KB limit: $ICON_FILE" >&2
    exit 1
  fi
done
if ! cmp -s "$SCRIPT_DIR/ICON.PNG" "$SCRIPT_DIR/app/ui/images/icon_64.png" || \
   ! cmp -s "$SCRIPT_DIR/ICON_256.PNG" "$SCRIPT_DIR/app/ui/images/icon_256.png"; then
  echo "fnOS package and desktop icons are out of sync" >&2
  exit 1
fi

if [ -z "$FNPACK_BIN" ] && command -v fnpack >/dev/null 2>&1; then
  FNPACK_BIN="$(command -v fnpack)"
fi
if [ -z "$FNPACK_BIN" ]; then
  case "$(uname -m)" in
    x86_64|amd64) FN_PACK_ARCH=amd64 ;;
    aarch64|arm64) FN_PACK_ARCH=arm64 ;;
    *) echo "Unsupported fnpack builder architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  TMP_DIR="$(mktemp -d)"
  FNPACK_BIN="$TMP_DIR/fnpack"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-${FN_PACK_ARCH}" -o "$FNPACK_BIN"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$FNPACK_BIN" "https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-${FN_PACK_ARCH}"
  else
    echo "fnpack is missing and curl/wget is unavailable" >&2
    exit 1
  fi
  chmod 755 "$FNPACK_BIN"
fi

WEB_DIST="${VELIN_FNOS_WEB_DIST:-$REPO_DIR/web/dist}"
if [ ! -f "$WEB_DIST/index.html" ]; then
  require_command npm
  (
    cd "$REPO_DIR/web"
    npm ci
    npm run build
  )
fi

STAGE_DIR="$(mktemp -d)"
cp -R "$SCRIPT_DIR"/. "$STAGE_DIR/"
rm -f "$STAGE_DIR/build.sh"
mkdir -p "$STAGE_DIR/app/bin" "$STAGE_DIR/app/web/dist" "$STAGE_DIR/app/native/guacd-root"
cp -R "$WEB_DIST"/. "$STAGE_DIR/app/web/dist/"

if [ -n "$PREBUILT_BINARY" ]; then
  if [ ! -f "$PREBUILT_BINARY" ]; then
    echo "Prebuilt Velin binary was not found: $PREBUILT_BINARY" >&2
    exit 1
  fi
  cp "$PREBUILT_BINARY" "$STAGE_DIR/app/bin/velin"
else
  require_command go
  (
    cd "$REPO_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build \
      -trimpath -ldflags="-s -w -X velin-webssh/internal/version.Current=${VERSION}" -o "$STAGE_DIR/app/bin/velin" ./cmd/velin
  )
fi

docker pull --platform "linux/$ARCH" "$GUACD_IMAGE" >/dev/null
CONTAINER_ID="$(docker create --platform "linux/$ARCH" "$GUACD_IMAGE")"
docker export "$CONTAINER_ID" | tar -xpf - -C "$STAGE_DIR/app/native/guacd-root" \
  opt/guacamole lib usr/lib etc/fonts etc/ssl/certs usr/share/fonts usr/share/fontconfig
docker rm "$CONTAINER_ID" >/dev/null
CONTAINER_ID=""

# The guacd image also contains build metadata, fonts and Ghostscript.
# Keep only runtime files needed by the native guacd process. fnOS extracts
# app.tgz into a temporary area, so avoid shipping the image's font bundle.
find "$STAGE_DIR/app/native/guacd-root" -type f \( -name '*.a' -o -name '*.la' \) -delete
find "$STAGE_DIR/app/native/guacd-root" -type d \( -name cmake -o -name pkgconfig \) -prune -exec rm -rf {} +
find "$STAGE_DIR/app/native/guacd-root/usr/lib" -maxdepth 1 -name 'libgs.so*' -delete
rm -rf "$STAGE_DIR/app/native/guacd-root/usr/share/fonts"
# fnOS rejects archives containing absolute symbolic links. These are only
# container font and certificate links; guacd uses the NAS system paths.
rm -rf "$STAGE_DIR/app/native/guacd-root/etc"
if find "$STAGE_DIR/app/native/guacd-root" -type l -lname '/*' -print -quit | grep -q .; then
  echo "guacd runtime contains unsupported absolute symbolic links" >&2
  exit 1
fi

LOADER="$(find "$STAGE_DIR/app/native/guacd-root/lib" -maxdepth 1 -name 'ld-musl-*.so.1' -type f -print -quit)"
if [ -z "$LOADER" ]; then
  echo "guacd runtime loader was not found in $GUACD_IMAGE" >&2
  exit 1
fi

chmod 755 "$STAGE_DIR/app/bin/velin"

cat > "$STAGE_DIR/app/bin/guacd" <<'EOF'
#!/bin/sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/../native/guacd-root" && pwd)"
LOADER="$(find "$ROOT/lib" -maxdepth 1 -name 'ld-musl-*.so.1' -type f -print -quit)"
if [ -z "$LOADER" ]; then
  echo "guacd runtime loader is missing" >&2
  exit 1
fi
LIBRARY_PATH="$ROOT/opt/guacamole/lib:$ROOT/lib:$ROOT/usr/lib"
exec "$LOADER" --library-path "$LIBRARY_PATH" \
  "$ROOT/opt/guacamole/sbin/guacd" -f -b 127.0.0.1 \
  -l "${VELIN_GUACD_PORT:-4822}" -L "${GUACD_LOG_LEVEL:-info}" "$@"
EOF
chmod 755 "$STAGE_DIR/app/bin/guacd"

sed -i "s/^version=.*/version=${VERSION}/" "$STAGE_DIR/manifest"
sed -i "s/^platform=.*/platform=${FNOS_PLATFORM}/" "$STAGE_DIR/manifest"

mkdir -p "$OUTPUT_DIR"
(
  cd "$STAGE_DIR"
  "$FNPACK_BIN" build --directory "$STAGE_DIR"
)
PACKAGE_FILE="$(find "$STAGE_DIR" -maxdepth 1 -type f -name '*.fpk' -print -quit)"
if [ -z "$PACKAGE_FILE" ]; then
  echo "fnpack did not produce an .fpk file" >&2
  exit 1
fi

# Catch incomplete archives before they are uploaded as release assets.
if ! tar -xOf "$PACKAGE_FILE" app.tgz | gzip -t; then
  echo "fnpack produced a corrupt app.tgz archive" >&2
  exit 1
fi
if ! tar -xOf "$PACKAGE_FILE" app.tgz | tar -tzf - >/dev/null; then
  echo "fnpack produced an invalid app.tgz tar archive" >&2
  exit 1
fi
for ICON_PATH in ui/images/icon_64.png ui/images/icon_256.png; do
  if ! tar -xOf "$PACKAGE_FILE" app.tgz | tar -tzf - | grep -Fxq "$ICON_PATH"; then
    echo "fnpack omitted required desktop icon: $ICON_PATH" >&2
    exit 1
  fi
done

OUTPUT_FILE="$OUTPUT_DIR/velin-fnos-native-${ARCH}-${VERSION}.fpk"
cp "$PACKAGE_FILE" "$OUTPUT_FILE"
printf '%s\n' "$OUTPUT_FILE"
