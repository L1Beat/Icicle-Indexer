# Icicle Deploy

Reproducible setup for an Icicle indexer + API server. Each new server is bootstrapped from the files in this directory; nothing lives only in someone's head.

## What's in here

| File | Role |
| --- | --- |
| `setup.sh` | Idempotent installer. Run once per fresh server. |
| `.env.example` | Template for the per-server secrets file. |
| `docker-compose.yml` | ClickHouse container with the right bind-mounts and loopback ports. |
| `clickhouse/users.d/default-user.xml.template` | Renders the `default` user with a sha256 password. |
| `clickhouse/access-setup.sql` | Creates the read-only `anonymous` / `anonymous_heavy` users for dashboards. |
| `nginx/api.conf` | Reverse-proxy vhost. Forwards `X-Forwarded-For` correctly. |
| `systemd/icicle-api.service`, `icicle-indexer.service`, `icicle-lending.service` | Service units (with memory caps). |
| `iptables/rules.v4` | Default-deny firewall. Allows 22/80/443/9651 only. |

## Bootstrap a new server (~20 minutes)

Assumes Ubuntu 24.04 with root SSH access and DNS already pointing your domain at the box.

```bash
# 1. Get the repo onto the server
ssh root@<server>
cd /root && git clone <repo-url> icicle && cd icicle/deploy

# 2. Create .env with real values
cp .env.example .env
nano .env
#    - Generate CLICKHOUSE_PASSWORD: openssl rand -base64 30 | tr -dc 'A-Za-z0-9' | head -c 32
#    - Compute CLICKHOUSE_PASSWORD_SHA256:
#        printf '%s' "$CLICKHOUSE_PASSWORD" | sha256sum | awk '{print $1}'
#    - Generate ICICLE_METRICS_TOKEN: openssl rand -hex 32
#    - Set API_DOMAIN to the domain you're using

# 3. Run the installer
./setup.sh
```

The installer:
1. Installs packages (Docker, nginx, certbot, iptables-persistent)
2. Loads firewall rules
3. Creates the `icicle` OS user
4. Renders the CH password into the bind-mounted users.d
5. Starts ClickHouse via docker-compose
6. Writes systemd env files (mode 600, including `lending.env` with the archive RPC) and unit files
7. Installs the nginx vhost (HTTP only — TLS in step 4 below)

After `setup.sh` finishes, do the four manual steps it prints:
1. Clone the Icicle source and `go build -o icicle .`
2. `clickhouse-client < clickhouse/access-setup.sql` to create the read-only users
3. `certbot --nginx -d $API_DOMAIN` to get a TLS cert (rewrites the nginx vhost)
4. `systemctl enable --now icicle-api icicle-indexer icicle-lending`

## Verifying a deploy

After everything is up:

```bash
systemctl is-active icicle-api icicle-indexer icicle-lending   # all 'active'
curl -fsS https://${API_DOMAIN}/health                # 200
sudo journalctl -u icicle-api -n 20 --no-pager        # 'ClickHouse connected successfully'
sudo journalctl -u icicle-lending -n 30 --no-pager    # 'protocol ready' then 'engine running'
curl -fsS http://127.0.0.1:9092/metrics | grep icicle_lending   # lending metrics present

# DB should NOT be reachable from outside (run from your laptop):
nc -zv <server-ip> 8123     # times out
nc -zv <server-ip> 9000     # times out

# Empty CH password should NOT work:
docker exec icicle-clickhouse clickhouse-client \
  --user default --password "" --query "SELECT 1"     # AUTHENTICATION_FAILED
```

## Updating the deployed app

```bash
cd ~/icicle && git pull && go build -o icicle . && sudo systemctl restart icicle-api icicle-indexer icicle-lending
```

## Rotating the ClickHouse password

```bash
# Pick a new password
NEW_PW=$(openssl rand -base64 30 | tr -dc 'A-Za-z0-9' | head -c 32)
HASH=$(printf '%s' "$NEW_PW" | sha256sum | awk '{print $1}')

# Update the host config
sudo sed -i "s|<password_sha256_hex>.*</password_sha256_hex>|<password_sha256_hex>$HASH</password_sha256_hex>|" \
  /home/icicle/clickhouse-config/users.d/default-user.xml

# CH auto-reloads users.d. Verify old password is rejected:
docker exec icicle-clickhouse clickhouse-client --user default --password "OLD_PW" --query "SELECT 1"

# Update the env files
sudo sed -i "s|^CLICKHOUSE_PASSWORD=.*|CLICKHOUSE_PASSWORD=$NEW_PW|" \
  /etc/icicle/api.env /etc/icicle/indexer.env /etc/icicle/lending.env

# Restart services
sudo systemctl restart icicle-api icicle-indexer icicle-lending
```

## Things this deploy does NOT include (yet)

- **Avalanchego** — assumed already running, either on this host or reachable via RPC. Add its installation separately if needed.
- **Observability stack** (Prometheus / Grafana / Loki) — operator-side, not customer-facing. See the main repo's `.docs/` for the setup that was used in the L1Beat dev environment.
- **ClickHouse backup** — only the indexer RPC cache is currently backed up (`backup_cache.sh`). Real CH backups via `clickhouse-backup` or volume snapshots are a TODO.
- **TLS cert auto-renewal** — certbot installs a systemd timer that handles renewals automatically; nothing to do here. Just confirm with `systemctl list-timers | grep certbot`.

## Security checklist after deploy

- [ ] `ss -tlnp | grep -E ':8123|:9000'` shows `127.0.0.1` only (never `0.0.0.0`)
- [ ] `docker exec icicle-clickhouse clickhouse-client --user default --password ""` fails with `AUTHENTICATION_FAILED`
- [ ] `nc -zv <server-ip> 8123` (from outside) times out
- [ ] `sudo iptables -L INPUT -n` shows default policy DROP
- [ ] `systemctl is-enabled netfilter-persistent` reports `enabled` (firewall survives reboots)
- [ ] `curl -sI https://<domain>/metrics` returns 401 (Prometheus auth gating works)
- [ ] In `journalctl -u icicle-api`, `HTTP request` lines contain `remote=` and `xff=` fields (IP logging works end-to-end)
