#!/bin/bash
# idlegrid provider installer for macOS (Apple Silicon).
#
#   curl -fsSL https://raw.githubusercontent.com/<REPO>/main/deploy/install.sh | bash -s -- \
#       --server wss://api.example.com/ws/provider --code <join-code>
#
# What it does:
#   1. Checks you're on Apple Silicon, macOS 14+
#   2. Downloads the latest provider release from GitHub (binary + mlx.metallib)
#   3. Verifies SHA-256 checksums
#   4. Installs to ~/.idlegrid/bin
#   5. Registers a LaunchAgent so it runs at login, restarts on crash,
#      and survives terminal closes
#
# Flags:
#   --server URL        coordinator WebSocket endpoint (required)
#   --code CODE         provider join code from the coordinator owner
#   --enroll-code CODE  bind this Mac to your console account (earnings flow to you)
#   --model ID          Hugging Face MLX model (default mlx-community/Qwen2.5-0.5B-Instruct-4bit)
#   --name NAME         node name (default: hostname)
#   --repo OWNER/NAME   GitHub repo holding releases (default: IDLEGRID_REPO env)
#   --release TAG       specific release tag (default: latest)
#   --uninstall         remove the provider and its LaunchAgent
set -euo pipefail

REPO="${IDLEGRID_REPO:-aiaakash/idlegrid}"
SERVER="" CODE="" MODEL="" NAME="" RELEASE="latest"
INSTALL_ROOT="$HOME/.idlegrid"
USE_LAUNCHD=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --code) CODE="$2"; shift 2 ;;
    --enroll-code) ENROLL_CODE="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --repo) REPO="$2"; shift 2 ;;
    --release) RELEASE="$2"; shift 2 ;;
    --base) BASE_OVERRIDE="$2"; shift 2 ;;
    --install-root) INSTALL_ROOT="$2"; shift 2 ;;
    --no-launchd) USE_LAUNCHD=0; shift ;;
    --uninstall) INSTALL_ROOT="${INSTALL_ROOT:-$HOME/.idlegrid}"; UNINSTALL=1; shift ;;
    *) echo "unknown flag: $1"; exit 2 ;;
  esac
done

LABEL="io.idlegrid.provider"
BIN_DIR="$INSTALL_ROOT/bin"

fail() { echo "  ✗ $*" >&2; exit 1; }
step() { echo "  → $*"; }

if [[ "${UNINSTALL:-0}" == "1" ]]; then
  step "uninstalling..."
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  rm -f "$HOME/Library/LaunchAgents/$LABEL.plist"
  rm -rf "$INSTALL_ROOT"
  echo "  ✓ removed"
  exit 0
fi

echo "idlegrid provider installer"

# 1. Platform checks
[[ "$(uname -s)" == "Darwin" ]] || fail "this installer is for macOS only"
[[ "$(uname -m)" == "arm64" ]] || fail "Apple Silicon (arm64) required, found: $(uname -m)"
MACVER=$(sw_vers -productVersion)
step "macOS $MACVER on $(uname -m) ✓"

# 2. Resolve release download URL
if [[ -n "${BASE_OVERRIDE:-}" ]]; then
  BASE="$BASE_OVERRIDE"            # testing override (local file server)
elif [[ "$RELEASE" == "latest" ]]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$RELEASE"
fi
step "release source: $BASE"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 3. Download + verify
step "downloading provider bundle..."
curl -fsSL "$BASE/idlegrid-provider-macos-arm64.zip" -o "$TMP/provider.zip" \
  || fail "download failed — does release exist in $REPO?"
curl -fsSL "$BASE/sha256sums.txt" -o "$TMP/sha256sums.txt" || fail "checksums missing"

step "verifying SHA-256..."
EXPECTED=$(grep "idlegrid-provider-macos-arm64.zip" "$TMP/sha256sums.txt" | awk '{print $1}')
[[ -n "$EXPECTED" ]] || fail "no checksum entry for provider zip"
ACTUAL=$(shasum -a 256 "$TMP/provider.zip" | cut -d' ' -f1)
[[ "$ACTUAL" == "$EXPECTED" ]] || fail "checksum mismatch (expected $EXPECTED, got $ACTUAL)"
echo "  ✓ checksum ok"

# 4. Install
step "installing to $BIN_DIR..."
mkdir -p "$BIN_DIR"
unzip -oq "$TMP/provider.zip" -d "$TMP/unpacked"
BIN_PATH=$(find "$TMP/unpacked" -name "idlegrid-provider" -type f | head -1)
LIB_PATH=$(find "$TMP/unpacked" -name "mlx.metallib" | head -1)
[[ -n "$BIN_PATH" && -n "$LIB_PATH" ]] || fail "bundle missing idlegrid-provider or mlx.metallib"
# CRITICAL: mlx.metallib must sit in the SAME directory as the binary.
cp "$BIN_PATH" "$BIN_DIR/idlegrid-provider"
cp "$LIB_PATH" "$BIN_DIR/mlx.metallib"
chmod +x "$BIN_DIR/idlegrid-provider"
echo "  ✓ installed"

# 5. Config file
step "writing $INSTALL_ROOT/config.env..."
cat > "$INSTALL_ROOT/config.env" <<EOF
IDLEGRID_SERVER='$SERVER'
IDLEGRID_CODE='$CODE'
IDLEGRID_ENROLL_CODE='$ENROLL_CODE'
IDLEGRID_MODEL='${MODEL:-mlx-community/Qwen2.5-0.5B-Instruct-4bit}'
IDLEGRID_NAME='${NAME:-$(hostname -s)}'
EOF

# 6. LaunchAgent
if [[ "$USE_LAUNCHD" == "1" ]]; then
  PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
  step "installing LaunchAgent ($PLIST)..."
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>$LABEL</string>
    <key>ProgramArguments</key>
    <array>
        <string>$BIN_DIR/idlegrid-provider</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>IDLEGRID_PROVIDER_ARGS</key><string>--coordinator \$IDLEGRID_SERVER --code \$IDLEGRID_CODE --model \$IDLEGRID_MODEL --name \$IDLEGRID_NAME</string>
    </dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>$INSTALL_ROOT/provider.log</string>
    <key>StandardErrorPath</key><string>$INSTALL_ROOT/provider.log</string>
</dict>
</plist>
EOF
  # NOTE: plist env-var interpolation doesn't work like shell; use a wrapper script instead.
  cat > "$BIN_DIR/run-provider.sh" <<EOF
#!/bin/bash
set -a
source "$INSTALL_ROOT/config.env"
exec "$BIN_DIR/idlegrid-provider" \\
  --coordinator "\$IDLEGRID_SERVER" \\
  \${IDLEGRID_CODE:+--code "\$IDLEGRID_CODE"} \\
  --model "\$IDLEGRID_MODEL" \\
  --name "\$IDLEGRID_NAME"
EOF
  chmod +x "$BIN_DIR/run-provider.sh"
  # replace ProgramArguments with the wrapper
  python3 - "$PLIST" "$BIN_DIR/run-provider.sh" <<'PYEOF'
import plistlib, sys
p = sys.argv[1]
with open(p, "rb") as f:
    d = plistlib.load(f)
d["ProgramArguments"] = [sys.argv[2]]
del d["EnvironmentVariables"]
with open(p, "wb") as f:
    plistlib.dump(d, f)
PYEOF
  launchctl bootstrap "gui/$(id -u)" "$PLIST" \
    || launchctl load "$PLIST" 2>/dev/null || true
  echo "  ✓ service started (runs at login, restarts on crash)"
  echo
  echo "Provider is running. Logs: $INSTALL_ROOT/provider.log"
  echo "Stop:    launchctl bootout gui/$(id -u)/$LABEL"
  echo "Remove:  $0 --uninstall"
else
  echo
  echo "Installed. Run manually:"
  echo "  source $INSTALL_ROOT/config.env"
  echo "  $BIN_DIR/idlegrid-provider --coordinator \"\$IDLEGRID_SERVER\" --code \"\$IDLEGRID_CODE\" --model \"\$IDLEGRID_MODEL\""
fi
