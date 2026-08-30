# Production deployment guide

Two supported paths:

- **Coolify (recommended if your server already runs it)** — see §A below.
  Docker-based, automatic TLS via the built-in proxy, git-push deploys.
- **Plain Ubuntu + systemd + Caddy** — see §B.

Target: any Ubuntu VM (Hostinger works). The coordinator is a stateless
single binary — 1 vCPU / 2 GB RAM is plenty to start.

```
developers ──HTTPS──▶ api.sqlguroo.com (TLS proxy) ──▶ coordinator :8090/:8080
Mac providers ──outbound WSS──────────────────────────────────┘
```

## 0. Prerequisites

- DNS: an `A` record `api` → server IP (TTL 300), at the registrar where
  sqlguroo.com is registered
- A GitHub release (tag push → CI builds it), or `scripts/release.sh vX` locally
- Server firewall: 22 + 80 + 443 open (Coolify also uses 8000/6001/6002 for its panel)

---

## §A. Deploying with Coolify

The repo root has a `Dockerfile` (multi-stage: Go build → tiny alpine image,
`/healthz` built in). The coordinator is stateless — no volumes needed.

1. **Connect GitHub** (once): Coolify → *Sources* → connect the GitHub App and
   grant access to `aiaakash/idlegrid`.
2. **New Resource** → *Dockerfile* → **Deploy via Dockerfile from a Git
   repository** → pick `aiaakash/idlegrid`, branch `main`, Dockerfile path `/Dockerfile`.
3. In the app's **Domain** field: `https://api.sqlguroo.com` — Coolify's proxy
   issues the Let's Encrypt certificate automatically (WebSocket routing is
   handled too).
4. **Environment Variables** (in the app settings):
   | Name | Value |
   |---|---|
   | `IDLEGRID_API_KEYS` | `openssl rand -hex 24` (admin keys) |
   | `IDLEGRID_PROVIDER_CODE` | `openssl rand -hex 8` (give to Mac owners) |
   | `PORT` | `8080` (image default) |
   | `DATABASE_URL` | see "Postgres" below — enables billing/metering |
   | `IDLEGRID_PLATFORM_FEE_PCT` | `10` (your revenue share; providers get 90%) |

5. **Postgres** (billing store — required for metering + per-user keys):
   - Coolify → **New Resource → Database → PostgreSQL** (same server, attach to the same project)
   - Copy its connection string into the coordinator's `DATABASE_URL` env var
     (e.g. `postgres://user:pass@<db-container-name>:5432/idlegrid?sslmode=disable`)
   - Migrations run automatically on coordinator start

6. **Health check** path: `/healthz` (image already defines it).
7. Press **Deploy**. First build ~1 min.
8. **Auto-deploy**: in the app's *Webhook* settings, copy the deploy webhook
   into the GitHub repo's Webhooks (or enable the GitHub App auto-deploy
   toggle). Now every push to `main` redeploys.
9. Verify: `curl -s https://api.sqlguroo.com/healthz` → `ok`, dashboard at
   `https://api.sqlguroo.com/`.

**Creating developer accounts** (with `DATABASE_URL` set):

```bash
curl -X POST https://api.sqlguroo.com/v1/admin/users \
  -H "Authorization: Bearer <ADMIN_KEY>" -H "Content-Type: application/json" \
  -d '{"email":"dev@example.com","label":"first dev"}'
# -> {"user_id":1,"api_key":"sk-ig-..."}  (shown once — hand it to the developer)
```

**Upgrade** = `git tag vX && git push origin vX` for releases, or just push to
`main` for auto-deploy. Macs reconnect automatically after restarts.

### Console (Next.js) on Coolify

The billing console lives in `console/` (Next.js). Deploy as a second resource:

1. New Resource → Git repository → `aiaakash/idlegrid`, branch `main`,
   **Build Pack: Dockerfile**? — No: the console has no Dockerfile yet; use
   **Docker Compose / Dockerfile** after adding one, or simplest today:
   `cd console && npm i && npm run build && npm start` behind a reverse proxy.
2. Domain: `https://console.sqlguroo.com`
3. Env: `CONSOLE_API_URL=http://<coordinator-container>:8080`,
   `NODE_ENV=production`

(First console user: `POST /v1/console/admin/users` with the admin API key —
see "Creating developer accounts" above; that endpoint also sets passwords.)

---

## §B. Plain Ubuntu (systemd + Caddy)

(unchanged — the classic path below)

Target: any Ubuntu 22.04/24.04 VM with a DNS name (tested plan for Hostinger).
The coordinator is lightweight — 1 vCPU / 2 GB RAM is plenty to start.

```
developers ──HTTPS──▶ api.sqlguroo.com (Caddy, TLS) ──▶ coordinator :8090
Mac providers ──outbound WSS──────────────────────────────────┘
```

## 0. Prerequisites

- A DNS name for the server (a bought domain, or a free one for testing — see below)
- A release downloaded from GitHub Releases (`idlegrid-coordinator-vX-linux-amd64.tar.gz`),
  or built locally with `scripts/release.sh vX`

### Testing WITHOUT buying a domain

The coordinator code has **no domain in it**. The domain appears in exactly
one functional file: the Caddyfile. So you can deploy today on a temporary
name and swap later by editing one line.

| Option | What you get | Effort |
|---|---|---|
| **A. Raw IP** | `http://SERVER_IP:8090` — providers use `ws://SERVER_IP:8090/ws/provider`, devs use `http://...`. No TLS, no Caddy needed | ~0 min |
| **B. sslip.io (recommended test)** | Free magic DNS: `api.203-0-113-10.sslip.io` (your IP, dots→dashes) resolves to your server. Caddy gets a **real Let's Encrypt certificate** for it — full production behavior | ~5 min |
| **C. trycloudflare** | `cloudflared tunnel --url http://localhost:8090` → instant HTTPS URL, no firewall changes. Random URL, changes on restart | ~2 min |

Option B is closest to production: put `api.YOUR-IP-DASHED.sslip.io` in the
Caddyfile instead of `api.example.com` — everything else in this guide is
identical.

**Swapping to the final product domain later:**

1. Buy/point the new domain: A record `api.newdomain.com` → same server IP
2. Edit the one line in `/etc/caddy/Caddyfile` (`api.sqlguroo.com` →
   `api.newdomain.com`) → `systemctl reload caddy`
3. Providers installed with the sqlguroo.com URL keep it in
   `~/.idlegrid/config.env` — re-run the one-line install command with the
   new URL (one line per Mac; do this swap before scaling to many Macs)

Nothing in the coordinator code changes — it has no domain in it.

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
curl -s https://api.sqlguroo.com/healthz      # -> ok
```

## 4. Upgrade to a new release

```bash
systemctl stop idlegrid
install -m 755 coordinator-new /opt/idlegrid/coordinator
systemctl start idlegrid
```

Providers reconnect automatically within ~30 s — no Mac-side action needed.

## 5. Verify

- `curl -s https://api.sqlguroo.com/debug/providers` — live node list (no auth)
- Playground/dashboard: `https://api.sqlguroo.com/`
- Inference: `curl https://api.sqlguroo.com/v1/chat/completions -H "Authorization: Bearer <key>" ...`

## Security checklist before inviting strangers

- [ ] `IDLEGRID_API_KEYS` are long random values (not `dev-key`)
- [ ] `IDLEGRID_PROVIDER_CODE` set (no anonymous providers)
- [ ] Requests are still plaintext inside the network — see roadmap; invite only people you trust until E2E encryption ships
- [ ] `ufw` open on 80/443 only
- [ ] Backups of `/etc/idlegrid/env`
