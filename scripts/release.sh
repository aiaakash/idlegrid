#!/bin/bash
# Builds release artifacts into dist/ — the proper way to ship:
#   dist/idlegrid-coordinator-vX-linux-amd64.tar.gz   (server side)
#   dist/idlegrid-provider-macos-arm64.zip            (Mac side)
#   dist/sha256sums.txt
#
# Usage: ./scripts/release.sh [version]     e.g. ./scripts/release.sh v0.3.0
# Then upload:  gh release create vX --generate-notes dist/*
#   (or attach the files to a Release in the GitHub web UI)
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: release.sh <version>  e.g. v0.3.0}"
DIST="dist"
rm -rf "$DIST"; mkdir -p "$DIST"

echo "==> building linux coordinator (amd64)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
    -ldflags "-s -w" \
    -o "$DIST/pkg/coordinator" ./coordinator/cmd/coordinator

echo "==> packaging coordinator tarball..."
cp deploy/coordinator.service deploy/Caddyfile deploy/idlegrid.env deploy/DEPLOY.md "$DIST/pkg/"
tar -czf "$DIST/idlegrid-coordinator-$VERSION-linux-amd64.tar.gz" -C "$DIST/pkg" .
rm -rf "$DIST/pkg"

echo "==> building macOS provider (arm64)..."
(cd provider-swift && ./build.sh)

echo "==> packaging provider zip..."
STAGE="$DIST/provider-pkg/idlegrid"
mkdir -p "$STAGE"
cp provider-swift/.build/release/idlegrid-provider "$STAGE/"
cp provider-swift/vendor/mlx.metallib "$STAGE/"
cat > "$STAGE/README.txt" <<'EOF'
idlegrid provider bundle.
idlegrid-provider and mlx.metallib MUST stay in the same directory.
Install with deploy/install.sh (recommended) or run manually:
  ./idlegrid-provider --coordinator wss://YOUR-SERVER/ws/provider --code YOUR-CODE
EOF
(cd "$DIST/provider-pkg" && zip -qr "$OLDPWD/$DIST/idlegrid-provider-macos-arm64.zip" idlegrid)
rm -rf "$DIST/provider-pkg"

echo "==> checksums..."
(cd "$DIST" && shasum -a 256 idlegrid-coordinator-*.tar.gz idlegrid-provider-*.zip > sha256sums.txt)

echo
echo "Release artifacts in $DIST/:"
ls -lh "$DIST" | awk 'NR>1 {print "  " $9 "  (" $5 ")"}'
echo
echo "Publish:"
echo "  gh release create $VERSION --title \"$VERSION\" --generate-notes $DIST/*"
echo "  (or Releases -> Draft new release in the GitHub web UI)"
