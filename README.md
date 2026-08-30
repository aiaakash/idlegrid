# idlegrid

Turn idle Macs into one private AI cloud. A small Go **coordinator** takes
OpenAI-compatible requests and routes them to **provider** apps running on
Macs (in-process MLX on the Apple GPU — no subprocess, no open ports,
debugger-blocked). Mac owners earn from idle hardware; developers just change
one URL in their OpenAI SDK.

```
OpenAI SDK ──HTTPS──▶ coordinator ──outbound WSS──▶ Macs running MLX
```

---

## Part 1 — Run and test locally (this is what we tested)

Everything below was run and verified on a MacBook Pro (M4 Pro, 24 GB) with a
MacBook Air as the second node.

### What you need

- Apple Silicon Mac, macOS 14+
- Xcode Command Line Tools 26.6+ (`swift --version` to check)
- Go 1.24+ (`go version` to check)
- Internet (model downloads once, ~500 MB)

### One-time setup

```bash
git clone https://github.com/aiaakash/idlegrid.git && cd idlegrid
# — or copy the folder, then:  cd idlegrid
make build        # builds coordinator + provider; first run vendors MLX (~10 min)
```

`make build` automatically: vendors Apple's mlx-swift with our no-JIT fix,
downloads the matching `mlx.metallib` from the official mlx wheel, and places
the metallib next to the binary (required — see "Common problems").

### Start the network (2 terminals)

```bash
# Terminal 1 — the coordinator (server + dashboard)
make run-coordinator

# Terminal 2 — this Mac as a provider (downloads model on first run)
make run-provider
```

Wait for `registered as ...` in terminal 2.

### Test it (4 ways, easiest first)

**1. Dashboard** — open `http://localhost:8090`: see the fleet, use the
streaming playground.

**2. curl:**

```bash
curl -s localhost:8090/v1/chat/completions \
  -H "Authorization: Bearer dev-key" -H "Content-Type: application/json" \
  -d '{"model":"Qwen2.5-0.5B-Instruct-4bit","messages":[{"role":"user","content":"What is 2+2?"}]}'
```

**3. Python (any OpenAI SDK app):**

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8090/v1", api_key="dev-key")
print(client.chat.completions.create(
    model="Qwen2.5-0.5B-Instruct-4bit",
    messages=[{"role": "user", "content": "Say hello"}],
).choices[0].message.content)
```

**4. Stress test with fake Macs** (no GPU work, simulates 8–200 nodes):

```bash
make run-fake N=50
```

### Automated tests

```bash
make test
```

Covers: scheduler behavior (load spreading, failure cooldown, stale sweeps)
and full HTTP→WebSocket round trips, including a provider dying mid-stream.

### Add a second Mac

```bash
# on Mac #2 — copy the repo (git clone once it's on GitHub), then:
make build
./provider-swift/.build/release/idlegrid-provider \
  --coordinator ws://MAC1-IP:8090/ws/provider \
  --code <join-code-if-set> \
  --name "Mac-2"
```

Find Mac 1's IP with `ipconfig getifaddr en0` (same Wi-Fi) — or use Tailscale
(recommended; it mirrors real-world NAT conditions).

**Failure drills we ran:** close the lid mid-generation (client gets a clean
error, node goes red on the dashboard, reconnects on wake), `kill -9` the
provider (llama-server-era orphan cleanup), coordinator sleep (providers
auto-reconnect with backoff).

### Stop / clean up

```bash
make stop     # kills coordinator, providers, orphans
```

---

## Part 2 — Production (Ubuntu server, e.g. Hostinger)

The proper path: **git → GitHub Releases → server pulls → Macs install with
one command**. No AirDrop, no scp of loose files.

### How the pieces ship

| Piece | Built by | Shipped as |
|---|---|---|
| Coordinator (Linux) | `scripts/release.sh` or GitHub Actions | `idlegrid-coordinator-vX-linux-amd64.tar.gz` (binary + systemd + Caddy + env templates) |
| Provider (macOS) | same | `idlegrid-provider-macos-arm64.zip` (binary + `mlx.metallib`) |
| Installer | `deploy/install.sh` | `curl ... \| bash` — downloads the zip, verifies SHA-256, installs a LaunchAgent |

### Step 1 — Put the repo on GitHub

```bash
cd ~/React\ Apps/idlegrid
git remote add origin https://github.com/aiaakash/idlegrid.git
git push -u origin main
```

### Step 2 — Cut a release

Either push a tag and let CI build everything (`/.github/workflows/release.yml`
builds the Linux coordinator on Ubuntu and the macOS provider on a Mac runner,
then publishes both to GitHub Releases):

```bash
git tag v0.3.0 && git push origin v0.3.0
```

…or build locally and publish yourself:

```bash
./scripts/release.sh v0.3.0
gh release create v0.3.0 --generate-notes dist/*      # brew install gh
# or: attach dist/* to a Release in the GitHub web UI
```

### Step 3 — Deploy the coordinator on the Ubuntu server

Full detail in [`deploy/DEPLOY.md`](deploy/DEPLOY.md). Short version:

```bash
ssh root@SERVER_IP
# DNS: A record api.yourdomain.com -> SERVER_IP, then:
apt install -y caddy
tar -xzf idlegrid-coordinator-v0.3.0-linux-amd64.tar.gz -C /tmp
install -m755 /tmp/coordinator /opt/idlegrid/coordinator
install -m644 /tmp/coordinator.service /etc/systemd/system/idlegrid.service
install -m644 /tmp/idlegrid.env /etc/idlegrid/env      # put REAL secrets in here
install -m644 /tmp/Caddyfile /etc/caddy/Caddyfile      # set your domain
systemctl enable --now caddy idlegrid
curl -s https://api.yourdomain.com/healthz             # -> ok
```

Set in `/etc/idlegrid/env` (generate with `openssl rand -hex 24` / `-hex 8`):

```
IDLEGRID_API_KEYS=<long-random>      # developer API keys
IDLEGRID_PROVIDER_CODE=<join-code>   # Mac owners must present this to join
```

### Step 4 — Mac owners join (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/YOUR-USER/idlegrid/main/deploy/install.sh \
  | bash -s -- --server wss://api.yourdomain.com/ws/provider \
      --code <join-code>
```

The installer verifies checksums, installs to `~/.idlegrid`, registers a
LaunchAgent (runs at login, restarts on crash), and connects. Uninstall:
`install.sh --uninstall`.

### Step 5 — Developers call the API

```python
client = OpenAI(base_url="https://api.yourdomain.com/v1", api_key="<IDLEGRID_API_KEYS value>")
```

Dashboard lives at `https://api.yourdomain.com/`.

---

## Common problems

| Problem | Fix |
|---|---|
| `address already in use` | `make stop`, retry |
| Provider `Could not connect` | coordinator running? IP right? Same network? |
| `REGISTRATION DENIED: invalid or missing join code` | `--code` must match the coordinator's `IDLEGRID_PROVIDER_CODE` |
| AI output is nonsense | `mlx.metallib` missing or version-mismatched — must sit next to the binary and match the vendored mlx (0.29.1). `make build` handles this |
| `swift build` fails on toolchain | CLT must be a clean 26.6+ install (corrupted partial updates caused this once — full reinstall fixed it) |
| First provider start is slow | downloading model weights (once) |

## Repo layout

```
protocol/                      wire contract (coordinator ↔ provider)
coordinator/                   Go control plane (+ fakeprovider simulator)
provider-swift/                Swift provider (in-process MLX)
  libs/mlx-swift/              vendored mlx (bootstrapped, no-JIT patched) — not in git
  vendor/mlx.metallib          Metal shaders (fetched, version-pinned) — not in git
deploy/                        systemd, Caddyfile, env template, install.sh, DEPLOY.md
scripts/                       release.sh, bootstrap-mlx.sh
docs/setup-stages.html         visual: 1 Mac → 2 Macs → production
```

## Honest status (before public launch)

1. Requests are **not yet encrypted** end-to-end — invite only people you trust
2. Join codes are shared secrets, not per-user accounts
3. No billing yet; coordinator state is in-memory (providers auto-reconnect after restarts)

Next up (roadmap): NaCl Box E2E encryption → Secure Enclave keys → per-user
accounts → metering + Stripe.
