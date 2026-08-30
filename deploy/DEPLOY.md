# Production deployment guide (Ubuntu server)

Target: any Ubuntu 22.04/24.04 VM with a DNS name (tested plan for Hostinger).
The coordinator is lightweight — 1 vCPU / 2 GB RAM is plenty to start.

```
developers ──HTTPS──▶ api.yourdomain.com (Caddy, TLS) ──▶ coordinator :8090
Mac providers ──outbound WSS──────────────────────────────────┘
```

## 0. Prerequisites

- A domain with an **A record** pointing to the server IP (e.g. `api.yourdomain.com`)
- A release downloaded from GitHub Releases (`idlegrid-coordinator-vX-linux-amd64.tar.gz`),
  or built locally with `scripts/release.sh vX`

## 1. Server setup (once)

```bash
ssh root@SERVER_IP
adduser --disabled-password --gecos "" idlegrid
mkdir -p /opt/idlegrid /etc/idlegrid /var/lib/idlegrid
chown -R idlegrid:idlegrid /opt/idlegrid /var/lib/idlegrid

# Caddy for automatic HTTPS
apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy

# firewall: web only
ufw allow OpenSSH && ufw allow 80,443/tcp && ufw --force enable
```

## 2. Install the coordinator

```bash
tar -xzf idlegrid-coordinator-vX-linux-amd64.tar.gz -C /tmp
install -m 755 /tmp/coordinator /opt/idlegrid/coordinator
install -m 644 /tmp/coordinator.service /etc/systemd/system/idlegrid.service
install -m 644 /tmp/idlegrid.env /etc/idlegrid/env
install -m 644 /tmp/Caddyfile /etc/caddy/Caddyfile   # edit domain first!
```

**Edit `/etc/idlegrid/env`** — generate real secrets first:

```bash
openssl rand -hex 24   # -> IDLEGRID_API_KEYS
openssl rand -hex 8    # -> IDLEGRID_PROVIDER_CODE (give this to Mac owners)
```

**Edit `/etc/caddy/Caddyfile`** — replace `api.example.com` with your domain.

## 3. Start

```bash
systemctl daemon-reload
systemctl enable --now caddy idlegrid
systemctl status idlegrid --no-pager
curl -s https://api.yourdomain.com/healthz      # -> ok
```

## 4. Upgrade to a new release

```bash
systemctl stop idlegrid
install -m 755 coordinator-new /opt/idlegrid/coordinator
systemctl start idlegrid
```

Providers reconnect automatically within ~30 s — no Mac-side action needed.

## 5. Verify

- `curl -s https://api.yourdomain.com/debug/providers` — live node list (no auth)
- Playground/dashboard: `https://api.yourdomain.com/`
- Inference: `curl https://api.yourdomain.com/v1/chat/completions -H "Authorization: Bearer <key>" ...`

## Security checklist before inviting strangers

- [ ] `IDLEGRID_API_KEYS` are long random values (not `dev-key`)
- [ ] `IDLEGRID_PROVIDER_CODE` set (no anonymous providers)
- [ ] Requests are still plaintext inside the network — see roadmap; invite only people you trust until E2E encryption ships
- [ ] `ufw` open on 80/443 only
- [ ] Backups of `/etc/idlegrid/env`
