#!/usr/bin/env bash
set -Eeuo pipefail

# Install the pinned Artalk sidecar used by Fileloom's local comments mode.
# This script is intentionally operator-run: Fileloom never invokes sudo from the CMS.

VERSION="2.10.0"
SITE_URL="${FILELOOM_BASE_URL:-}"
SITE_KEY="fileloom-site"
PORT="23366"
NO_START=0
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -z "$SITE_URL" && -f "${SCRIPT_DIR}/../.env" ]]; then
  SITE_URL="$(sed -n 's/^FILELOOM_BASE_URL=//p' "${SCRIPT_DIR}/../.env" | head -n 1)"
fi

usage() {
  cat <<'EOF'
Usage: sudo ./scripts/install-artalk.sh [options]

Installs or upgrades the pinned Artalk Go binary as fileloom-artalk.service.
The operation is idempotent: the Artalk database, app key, and existing config are preserved.

Options:
  --version VERSION       Artalk release version without the leading v (default: 2.10.0)
  --site-url URL          Public Fileloom origin used by Artalk (required unless FILELOOM_BASE_URL or repo .env is set)
  --site-key KEY          Stable Artalk site identifier (default: fileloom-site)
  --port PORT             Loopback Artalk port (default: 23366)
  --no-start              Install files but do not enable/restart the systemd service
  -h, --help              Show this help

After installation, open the Fileloom CMS Comments panel, choose local mode, and enable comments
only after the Artalk administrator and moderation settings are ready.
EOF
}

fail() {
  echo "install-artalk: $*" >&2
  exit 1
}

while (($#)); do
  case "$1" in
    --version)
      (($# >= 2)) || fail "--version requires a value"
      VERSION="${2#v}"
      shift 2
      ;;
    --site-url)
      (($# >= 2)) || fail "--site-url requires a value"
      SITE_URL="$2"
      shift 2
      ;;
    --site-key)
      (($# >= 2)) || fail "--site-key requires a value"
      SITE_KEY="$2"
      shift 2
      ;;
    --port)
      (($# >= 2)) || fail "--port requires a value"
      PORT="$2"
      shift 2
      ;;
    --no-start)
      NO_START=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[[ "${EUID}" -eq 0 ]] || fail "run this command with sudo"
command -v curl >/dev/null || fail "curl is required"
command -v install >/dev/null || fail "install is required"
command -v sha256sum >/dev/null || fail "sha256sum is required"
command -v systemctl >/dev/null || fail "systemd/systemctl is required"
command -v tar >/dev/null || fail "tar is required"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "version must look like 2.10.0"
[[ "$SITE_KEY" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || fail "site key must use 1-64 letters, numbers, dots, underscores, or hyphens"
[[ "$PORT" =~ ^[0-9]+$ ]] && ((PORT >= 1024 && PORT <= 65535)) || fail "port must be between 1024 and 65535"
[[ "$SITE_URL" =~ ^https?://[^[:space:]]+$ ]] || fail "site-url must be an absolute http(s) URL"

SITE_ORIGIN="$(printf '%s' "$SITE_URL" | sed -E 's#^(https?://[^/]+).*#\1#')"
[[ "$SITE_ORIGIN" =~ ^https?://[^/]+$ ]] || fail "site-url must contain a host"

case "$(uname -m)" in
  x86_64|amd64)
    ARTALK_ARCH="amd64"
    ;;
  aarch64|arm64)
    ARTALK_ARCH="arm64"
    ;;
  armv7l|armv7)
    ARTALK_ARCH="arm7"
    ;;
  *)
    fail "unsupported Linux architecture: $(uname -m)"
    ;;
esac

ARTALK_ASSET="artalk_v${VERSION}_linux_${ARTALK_ARCH}.tar.gz"
ARTALK_RELEASE="https://github.com/ArtalkJS/Artalk/releases/download/v${VERSION}"
PREFIX="/usr/local/libexec/fileloom-artalk"
VERSION_DIR="${PREFIX}/v${VERSION}"
CONFIG_DIR="/etc/fileloom-artalk"
DATA_DIR="/var/lib/fileloom-artalk"
SERVICE_FILE="/etc/systemd/system/fileloom-artalk.service"
ENV_FILE="${CONFIG_DIR}/artalk.env"
CONFIG_FILE="${CONFIG_DIR}/artalk.yml"
ARTALK_USER="artalk"
ARTALK_GROUP="artalk"

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

mkdir -p "$PREFIX" "$CONFIG_DIR" "$DATA_DIR/data" "$DATA_DIR/artalk-img"
chmod 0755 "$PREFIX"
chmod 0755 "$CONFIG_DIR"
chmod 0750 "$DATA_DIR" "$DATA_DIR/data" "$DATA_DIR/artalk-img"

if ! getent group "$ARTALK_GROUP" >/dev/null; then
  groupadd --system "$ARTALK_GROUP"
fi
if ! id "$ARTALK_USER" >/dev/null 2>&1; then
  useradd --system --gid "$ARTALK_GROUP" --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin "$ARTALK_USER"
fi

curl --fail --location --silent --show-error "${ARTALK_RELEASE}/${ARTALK_ASSET}" -o "${TMP_DIR}/${ARTALK_ASSET}"
curl --fail --location --silent --show-error "${ARTALK_RELEASE}/checksums.txt" -o "${TMP_DIR}/checksums.txt"
EXPECTED_SHA="$(awk -v asset="$ARTALK_ASSET" '$2 == asset {print $1; exit}' "${TMP_DIR}/checksums.txt")"
[[ "$EXPECTED_SHA" =~ ^[0-9a-fA-F]{64}$ ]] || fail "release checksums.txt did not contain ${ARTALK_ASSET}"
printf '%s  %s\n' "$EXPECTED_SHA" "${TMP_DIR}/${ARTALK_ASSET}" | sha256sum --check --status || fail "checksum verification failed for ${ARTALK_ASSET}"

tar -xzf "${TMP_DIR}/${ARTALK_ASSET}" -C "$TMP_DIR"
DOWNLOADED_BIN="${TMP_DIR}/artalk_v${VERSION}_linux_${ARTALK_ARCH}/artalk"
[[ -x "$DOWNLOADED_BIN" ]] || fail "release archive did not contain the Artalk binary"

mkdir -p "$VERSION_DIR"
install -o root -g root -m 0755 "$DOWNLOADED_BIN" "${VERSION_DIR}/artalk"
ln -sfn "v${VERSION}/artalk" "${PREFIX}/artalk"

if [[ ! -e "$CONFIG_FILE" ]]; then
  cat >"${CONFIG_FILE}.tmp" <<EOF
# Fileloom-managed Artalk baseline. Edit this file for provider-specific settings.
# Site-specific values are kept in artalk.env so rerunning the installer is idempotent.
db:
  type: sqlite
  file: ${DATA_DIR}/data/artalk.db
log:
  enabled: true
  filename: ${DATA_DIR}/data/artalk.log
img_upload:
  enabled: false
  path: ${DATA_DIR}/artalk-img
EOF
  install -o root -g root -m 0644 "${CONFIG_FILE}.tmp" "$CONFIG_FILE"
  rm -f "${CONFIG_FILE}.tmp"
fi

APP_KEY=""
if [[ -f "$ENV_FILE" ]]; then
  APP_KEY="$(sed -n 's/^ATK_APP_KEY=//p' "$ENV_FILE" | head -n 1 | tr -d '\"' || true)"
fi
if [[ ! "$APP_KEY" =~ ^[0-9a-fA-F]{64}$ ]]; then
  APP_KEY="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
fi

cat >"${ENV_FILE}.tmp" <<EOF
ATK_HOST=127.0.0.1
ATK_PORT=${PORT}
ATK_APP_KEY=${APP_KEY}
ATK_SITE_DEFAULT=${SITE_KEY}
ATK_SITE_URL=${SITE_ORIGIN}
ATK_TIMEZONE=UTC
ATK_TRUSTED_DOMAINS=${SITE_ORIGIN}
ATK_HTTP_PROXY_HEADER=X-Forwarded-For
ATK_DB_TYPE=sqlite
ATK_DB_FILE=${DATA_DIR}/data/artalk.db
ATK_LOG_ENABLED=true
ATK_LOG_FILENAME=${DATA_DIR}/data/artalk.log
ATK_IMG_UPLOAD_ENABLED=false
ATK_IMG_UPLOAD_PATH=${DATA_DIR}/artalk-img
EOF
install -o root -g root -m 0600 "${ENV_FILE}.tmp" "$ENV_FILE"
rm -f "${ENV_FILE}.tmp"

install -o root -g root -m 0644 "${SCRIPT_DIR}/../deploy/fileloom-artalk.service" "$SERVICE_FILE"

chown -R "${ARTALK_USER}:${ARTALK_GROUP}" "$DATA_DIR"
systemctl daemon-reload
if [[ "$NO_START" -eq 0 ]]; then
  if systemctl is-active --quiet fileloom-artalk.service; then
    systemctl restart fileloom-artalk.service
  else
    systemctl enable --now fileloom-artalk.service
  fi
  systemctl is-active --quiet fileloom-artalk.service || {
    systemctl --no-pager --full status fileloom-artalk.service >&2 || true
    fail "fileloom-artalk.service did not start"
  }
fi

echo "Artalk ${VERSION} is installed at ${PREFIX}/artalk"
echo "Service: fileloom-artalk.service"
echo "Loopback address: 127.0.0.1:${PORT}"
echo "Data: ${DATA_DIR}"
echo "Fileloom public proxy: /_fileloom/artalk"
echo "Next: create an Artalk admin account, then choose local mode in the Fileloom Comments panel."
