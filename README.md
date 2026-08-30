# idlegrid — simple guide

This project turns idle Macs into one big AI server.

- A **coordinator** (small Go server) takes AI requests.
- **Providers** (small apps on Macs) do the AI work using the Mac's own GPU (MLX).
- Anyone can call it with the normal **OpenAI SDK** — just change the URL.

You can run everything on ONE Mac. Later, add more Macs. This guide shows every step.

---

## 1. What you need

| Need | Details |
|---|---|
| A Mac | Apple Silicon (M1 or newer). macOS 14 or newer |
| Xcode Command Line Tools | For building the Mac app. Version 26.6 or newer |
| Go | For the coordinator server. Version 1.24 or newer |
| Internet | To download one model file (~500 MB, only once) |

Check your tools work:

```bash
go version
swift --version
```

---

## 2. One-time setup (5 minutes)

Open Terminal and go to the project folder:

```bash
cd ~/React\ Apps/idlegrid
```

Build everything (the Go server and the Mac app):

```bash
make build
```

The first build takes a few minutes. It also downloads the AI model when you
first run the provider (see step 3).

---

## 3. Run it (the easy way)

You need **2 terminal windows**.

**Terminal 1 — start the coordinator:**

```bash
make run-coordinator
```

You will see: `listening on :8090`. Keep this window open.

**Terminal 2 — start the provider (this Mac does the AI work):**

```bash
make run-provider
```

The first time, it downloads the model (~500 MB). When you see
`registered as ...` — this Mac is now part of the network. Keep this window open.

---

## 4. Test it

### Easiest test — the dashboard

Open your browser:

```
http://localhost:8090
```

You will see:
- Your Mac listed under **Fleet** (green dot = healthy)
- A **Playground** box. Type a message, press **Send**, and watch the answer
  stream in.

### Test with curl

```bash
curl -s localhost:8090/v1/chat/completions \
  -H "Authorization: Bearer dev-key" -H "Content-Type: application/json" \
  -d '{"model":"Qwen2.5-0.5B-Instruct-4bit","messages":[{"role":"user","content":"What is 2+2?"}]}'
```

### Test with Python (like any OpenAI app)

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8090/v1", api_key="dev-key")

answer = client.chat.completions.create(
    model="Qwen2.5-0.5B-Instruct-4bit",
    messages=[{"role": "user", "content": "Say hello"}],
)
print(answer.choices[0].message.content)
```

### Test fake Macs (no real work done, just to stress the server)

```bash
make run-fake N=8
```

This adds 8 fake providers. Refresh the dashboard and watch the Fleet list grow.

---

## 5. Stop everything

Press `Ctrl+C` in each terminal window, or run this from any terminal:

```bash
make stop
```

`make stop` also kills anything stuck. If you ever see
`address already in use`, run `make stop` and try again.

---

## 6. Add a second Mac (for example, a MacBook Air)

The second Mac only runs the provider. The coordinator stays on the first Mac.

**On the first Mac** — find its address:

```bash
ipconfig getifaddr en0
```

This prints something like `192.168.29.189`. Both Macs must be on the same
Wi-Fi network (or connected with Tailscale — even better, because it works
like the real internet).

**On the second Mac** — copy this project folder over (AirDrop, USB, or git),
then run:

```bash
cd idlegrid
make build
./provider-swift/.build/release/idlegrid-provider \
  --coordinator ws://FIRST-MAC-IP:8090/ws/provider \
  --name "M1-Air"
```

Replace `FIRST-MAC-IP` with the address from step above (example: `192.168.29.189`).

Now open the dashboard on the first Mac. You should see **two** Macs in the
Fleet list. Send a few messages — requests will jump between them.

**Fun test:** send a long message and close the second Mac's lid in the middle.
You will see the network handle the failure — that is the hard part of this
product, and it works.

---

## 7. Common problems

| Problem | Fix |
|---|---|
| `address already in use` | Run `make stop`, then start again |
| Provider says `Could not connect` | Is the coordinator running? Is the IP correct? Same Wi-Fi? |
| Provider is offline in dashboard | It reconnects by itself within ~30 seconds. Check its terminal |
| `swift build` fails | Update Command Line Tools (see below) |
| Build works but AI gives nonsense | The `mlx.metallib` file is missing or wrong version. It must sit next to the app — `make build` copies it |

---

## 8. Settings you can change

Coordinator (server):

```bash
PORT=8090                            # change the port
IDLEGRID_API_KEYS=dev-key,my-secret  # comma-separated API keys
```

Provider (the Mac app):

```bash
--coordinator URL    # where the coordinator is (default: this Mac)
--model ID           # which model to run (default: Qwen2.5-0.5B-Instruct-4bit)
--max-tokens 256     # max answer length
--name "My Mac"      # name shown on the dashboard
--dry-run            # join without running a model (for testing)
```

---

## 9. Run the automatic tests

```bash
make test
```

These tests cover the scheduler (picking Macs, spreading load, handling
failures) and full requests from the API to a fake provider and back.

---

## 10. What is in each folder

```
protocol/                      the shared message format (server ↔ Mac app)
coordinator/
  cmd/coordinator/             the server program
  cmd/fakeprovider/            fake Macs for load testing
  internal/registry/           list of Macs + the scheduler (who gets which job)
  internal/server/             OpenAI API + WebSocket hub + dashboard
provider-swift/                the Mac app (Swift + MLX, runs the model in-process)
  libs/mlx-swift/              Apple's MLX, with one small fix (see README note)
  vendor/mlx.metallib          GPU shader file that must sit next to the app
docs/setup-stages.html         visual guide: 1 Mac → 2 Macs → production
```

---

## 11. What is NOT done yet (the honest list)

1. Requests are **not encrypted** yet — do not send real private data
2. Anyone who knows the URL can join as a provider — no join codes yet
3. No payments or billing yet
4. Server state is in memory — restart clears the list of Macs (they reconnect)

The full plan for fixing these is at the bottom of `docs/setup-stages.html`.
