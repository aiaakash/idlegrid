# Deploy idlegrid on Coolify — from zero to live

Simple steps. Everything runs on your own server. No paid services.

---

## Before you start

- Coolify is installed on your server (you have this)
- Your GitHub repo exists: `https://github.com/aiaakash/idlegrid`
- You know your **server IP** (Hostinger panel → VPS → IP)

## Step 0 — Point the domains (5 minutes, do first)

In the DNS settings of sqlguroo.com (at your registrar), add two records:

| Type | Name | Value | TTL |
|---|---|---|---|
| A | `api` | your server IP | 300 |
| A | `console` | your server IP | 300 |

Done. Coolify will handle HTTPS certificates automatically later.

## Step 1 — Create the database (2 minutes)

1. Coolify → your project → **+ New Resource**
2. Choose **Database → PostgreSQL**
3. Pick the latest version → **Deploy**
4. Wait for green "running"
5. Open the database resource → find the **connection string**. It looks like:
   `postgres://postgres:BIGPASSWORD@<some-id>:5432/postgres`
6. **Copy it** — you need it in Step 2

> Tip: in the database settings, set **Container Name** to `idlegrid-db`.
> Then the connection string host becomes `idlegrid-db` — much easier to read.

## Step 2 — Deploy the coordinator (the brain)

1. **+ New Resource** → **Git Based → Public Repository**
2. Repository URL: `https://github.com/aiaakash/idlegrid.git` — Branch: `main`
3. Build Pack: **Dockerfile** — Dockerfile Location: `/Dockerfile`
4. After the config loads:
   - **Domain**: `https://api.sqlguroo.com`
   - (Coolify writes `https://` — keep it, TLS is automatic)
5. **Environment Variables** — add these 4:

```
IDLEGRID_API_KEYS=c9a196e2f0970af9ef99c0c1a0f9476cfc7a085c8c08db7b
IDLEGRID_PROVIDER_CODE=d5f62e95ee436aa5
IDLEGRID_PLATFORM_FEE_PCT=10
DATABASE_URL=<paste the connection string from Step 1>
```

6. **Deploy**. First build ~1–2 minutes.
7. Test: open `https://api.sqlguroo.com/healthz` — you want `ok`.
   The dashboard is at `https://api.sqlguroo.com/`.

## Step 3 — Deploy the console (the web app)

The console uses the **Docker Compose** build pack with a compose file at the
repo root (Coolify has a bug with Dockerfile/compose files inside
subdirectories — the helper's `mkdir -p` collides with the existing file).

1. **+ New Resource** → **Git Based → Public Repository** → same repo, branch `main`
2. Build Pack: **Docker Compose** — Compose Location: `/docker-compose.yml` (the file at the repo root — leave the default)
3. **Domain**: `https://console.sqlguroo.com`
4. **Environment Variables** — add one:

```
CONSOLE_API_URL=http://idlegrid-api:8080
```

> `idlegrid-api` = the coordinator. In Step 2, open the coordinator resource
> and set its **Container Name** to `idlegrid-api` (if you didn't, use the
> name Coolify shows instead).

5. **Deploy**. Test: `https://console.sqlguroo.com` shows a login page.

## Step 4 — Create your admin login (1 command)

From any terminal:

```bash
curl -X POST https://api.sqlguroo.com/v1/console/admin/users \
  -H "Authorization: Bearer IDLEGRID_API_KEYS_VALUE" \
  -H "Content-Type: application/json" \
  -d '{"email":"you@sqlguroo.com","password":"choose-a-password","role":"admin"}'
```

(Replace `IDLEGRID_API_KEYS_VALUE` with the value from Step 2.)

Now log in at `https://console.sqlguroo.com`.

## Step 5 — Turn on your first Mac (your own)

On your Mac:

```bash
curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash -s -- \
  --server wss://api.sqlguroo.com/ws/provider \
  --code IDLEGRID_PROVIDER_CODE_VALUE
```

The Mac connects automatically. Check:
- `https://api.sqlguroo.com` → Fleet table → your Mac with a green dot

## Step 6 — Test a real request

In the console: **API Keys → Create** → copy the key.
In the dashboard playground (`https://api.sqlguroo.com`) paste the key, send a
message. Tokens stream from your Mac's GPU. Usage and costs appear in the
console **Usage** page.

---

## Later — connect Dodo Payments (real deposits)

1. Create a Dodo Payments account, create one **product** called
   "idlegrid credit" (price $1, quantity used as multiplier)
2. Add these env vars to the **coordinator** (Coolify) and redeploy:

```
IDLEGRID_DODO_API_KEY=sk_live_xxx
IDLEGRID_DODO_WEBHOOK_SECRET=whsec_xxx
IDLEGRID_DODO_PRODUCT_ID=prd_xxx
IDLEGRID_DODO_RETURN_URL=https://console.sqlguroo.com
```

3. In Dodo's dashboard, set the **webhook URL** to:
   `https://api.sqlguroo.com/v1/dodo/webhook`
4. The console **Top-up** page now takes real payments and credits balances.

---

## Auto-deploy on every git push

Coolify → coordinator app → **Webhooks** → copy the "Deploy webhook" URL →
GitHub repo → Settings → Webhooks → Add webhook → paste URL.
Same for the console app. Now `git push` = automatic redeploy.

---

## Problems?

| Problem | Fix |
|---|---|
| Build fails on GitHub Actions | Check the Actions tab; errors so far were all CI-environment fixes already in main |
| `healthz` not reachable | DNS not propagated yet — wait a few minutes, check the A record |
| Console shows "unauthorized" everywhere | Your session expired — log in again |
| Provider says `REGISTRATION DENIED` | `--code` must exactly match `IDLEGRID_PROVIDER_CODE` in Coolify (no quotes/spaces) |
| Provider says `insufficient balance` | That's the balance guard — top up via console, or set `IDLEGRID_REQUIRE_BALANCE=0` for testing |
| Database "connection refused" | DATABASE_URL host must be the container name, not localhost |

---

## What you pay for

Nothing extra. Postgres, the coordinator, the console, and your providers all
run on the server you already have. The only future costs: the domain (when
you buy the final one) and Dodo's payment % on real deposits.
