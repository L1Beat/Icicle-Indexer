#!/usr/bin/env bash
# Provisions a fresh Ubuntu 24.04 server for the Icicle indexer service.
# Idempotent — safe to re-run.
#
# Prereqs:
#   - Fresh Ubuntu 24.04 (or compatible) with root access
#   - .env file in this directory (copy .env.example and fill in)
#
# Usage:
#   sudo ./setup.sh

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
  echo "Run as root: sudo $0" >&2
  exit 1
fi

cd "$(dirname "$0")"

if [ ! -f .env ]; then
  echo "Missing .env — copy .env.example to .env and fill in values" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

require() {
  for v in "$@"; do
    if [ -z "${!v:-}" ]; then
      echo "Missing required env var: $v" >&2
      exit 1
    fi
  done
}
require API_DOMAIN ICICLE_USER CLICKHOUSE_PASSWORD CLICKHOUSE_PASSWORD_SHA256 ICICLE_METRICS_TOKEN ICICLE_ARCHIVE_RPC

ICICLE_HOME="/home/${ICICLE_USER}"
CH_DATA_DIR="${CH_DATA_DIR:-${ICICLE_HOME}/clickhouse-data}"
CH_CONFIG_DIR="${CH_CONFIG_DIR:-${ICICLE_HOME}/clickhouse-config}"

echo "==> Installing system packages"
apt-get update -y
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  ca-certificates curl gnupg lsb-release \
  nginx certbot python3-certbot-nginx \
  iptables-persistent jq

if ! command -v docker >/dev/null 2>&1; then
  echo "==> Installing Docker"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
fi

echo "==> Installing firewall rules"
install -m 0644 iptables/rules.v4 /etc/iptables/rules.v4
systemctl enable --now netfilter-persistent
iptables-restore < /etc/iptables/rules.v4

if ! id "$ICICLE_USER" >/dev/null 2>&1; then
  echo "==> Creating user $ICICLE_USER"
  useradd -m -s /bin/bash "$ICICLE_USER"
fi
usermod -aG docker "$ICICLE_USER"

echo "==> Setting up ClickHouse config"
install -d -o "$ICICLE_USER" -g "$ICICLE_USER" "$CH_DATA_DIR"
install -d -o "$ICICLE_USER" -g "$ICICLE_USER" "$CH_CONFIG_DIR/users.d"

sed "s|__CLICKHOUSE_PASSWORD_SHA256__|$CLICKHOUSE_PASSWORD_SHA256|" \
  clickhouse/users.d/default-user.xml.template \
  > "$CH_CONFIG_DIR/users.d/default-user.xml"
chown "$ICICLE_USER:$ICICLE_USER" "$CH_CONFIG_DIR/users.d/default-user.xml"
chmod 644 "$CH_CONFIG_DIR/users.d/default-user.xml"

echo "==> Starting ClickHouse"
# Render .env paths into a tmp .env that docker compose picks up
TMP_ENV=$(mktemp)
{
  echo "CH_DATA_DIR=$CH_DATA_DIR"
  echo "CH_CONFIG_DIR=$CH_CONFIG_DIR"
} > "$TMP_ENV"
sudo -u "$ICICLE_USER" docker compose --env-file "$TMP_ENV" -f docker-compose.yml up -d
rm "$TMP_ENV"

echo "==> Installing secret env files"
install -d -m 700 /etc/icicle
{
  echo "CLICKHOUSE_PASSWORD=$CLICKHOUSE_PASSWORD"
  echo "ICICLE_METRICS_TOKEN=$ICICLE_METRICS_TOKEN"
} > /etc/icicle/api.env
chmod 600 /etc/icicle/api.env

echo "CLICKHOUSE_PASSWORD=$CLICKHOUSE_PASSWORD" > /etc/icicle/indexer.env
chmod 600 /etc/icicle/indexer.env

# Lending engine reads health from the archive node via Multicall3. The fallback
# RPC is optional and only used when the archive node fails.
{
  echo "CLICKHOUSE_PASSWORD=$CLICKHOUSE_PASSWORD"
  echo "ICICLE_ARCHIVE_RPC=$ICICLE_ARCHIVE_RPC"
  [ -n "${ICICLE_FALLBACK_RPC:-}" ] && echo "ICICLE_FALLBACK_RPC=$ICICLE_FALLBACK_RPC"
} > /etc/icicle/lending.env
chmod 600 /etc/icicle/lending.env

echo "==> Installing systemd units"
for unit in icicle-api.service icicle-indexer.service icicle-lending.service; do
  sed "s|__USER__|$ICICLE_USER|g; s|__HOME__|$ICICLE_HOME|g" "systemd/$unit" \
    > "/etc/systemd/system/$unit"
done
# Apply any per-deployment overrides if .env defines them
if [ -n "${ICICLE_API_MEMORY_HIGH:-}" ] || [ -n "${ICICLE_API_MEMORY_MAX:-}" ]; then
  install -d /etc/systemd/system/icicle-api.service.d
  {
    echo "[Service]"
    [ -n "${ICICLE_API_MEMORY_HIGH:-}" ] && echo "MemoryHigh=$ICICLE_API_MEMORY_HIGH"
    [ -n "${ICICLE_API_MEMORY_MAX:-}" ]  && echo "MemoryMax=$ICICLE_API_MEMORY_MAX"
  } > /etc/systemd/system/icicle-api.service.d/memory.conf
fi
if [ -n "${ICICLE_INDEXER_MEMORY_HIGH:-}" ] || [ -n "${ICICLE_INDEXER_MEMORY_MAX:-}" ]; then
  install -d /etc/systemd/system/icicle-indexer.service.d
  {
    echo "[Service]"
    [ -n "${ICICLE_INDEXER_MEMORY_HIGH:-}" ] && echo "MemoryHigh=$ICICLE_INDEXER_MEMORY_HIGH"
    [ -n "${ICICLE_INDEXER_MEMORY_MAX:-}" ]  && echo "MemoryMax=$ICICLE_INDEXER_MEMORY_MAX"
  } > /etc/systemd/system/icicle-indexer.service.d/memory.conf
fi
if [ -n "${ICICLE_LENDING_MEMORY_HIGH:-}" ] || [ -n "${ICICLE_LENDING_MEMORY_MAX:-}" ]; then
  install -d /etc/systemd/system/icicle-lending.service.d
  {
    echo "[Service]"
    [ -n "${ICICLE_LENDING_MEMORY_HIGH:-}" ] && echo "MemoryHigh=$ICICLE_LENDING_MEMORY_HIGH"
    [ -n "${ICICLE_LENDING_MEMORY_MAX:-}" ]  && echo "MemoryMax=$ICICLE_LENDING_MEMORY_MAX"
  } > /etc/systemd/system/icicle-lending.service.d/memory.conf
fi
systemctl daemon-reload

echo "==> Installing nginx vhost (without TLS yet)"
sed "s|__API_DOMAIN__|$API_DOMAIN|g" nginx/api.conf > /etc/nginx/sites-available/api
ln -sf /etc/nginx/sites-available/api /etc/nginx/sites-enabled/api
nginx -t
systemctl reload nginx

cat <<EOF

================================================================
 Initial setup complete. Next, manual steps:

  1. Clone Icicle repo and build the binary:
       sudo -u $ICICLE_USER bash -c 'cd $ICICLE_HOME && git clone <repo-url> icicle'
       sudo -u $ICICLE_USER bash -c 'cd $ICICLE_HOME/icicle && go build -o icicle .'

  2. Apply the read-only ClickHouse users (frontend access):
       docker exec -i icicle-clickhouse clickhouse-client \\
         --user default --password "$CLICKHOUSE_PASSWORD" \\
         < clickhouse/access-setup.sql

  3. Issue the TLS cert (DNS must already point $API_DOMAIN at this server):
       certbot --nginx -d $API_DOMAIN

  4. Start the services:
       systemctl enable --now icicle-api icicle-indexer icicle-lending

  5. Verify:
       systemctl is-active icicle-api icicle-indexer icicle-lending
       curl -fsS https://$API_DOMAIN/health
       sudo journalctl -u icicle-api -n 20 --no-pager
       sudo journalctl -u icicle-lending -n 20 --no-pager   # 'lending: engine running'
================================================================
EOF
