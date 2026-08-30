#!/bin/bash
# Builds the provider (SwiftPM + MLX) and colocates the Metal kernel library.
# mlx.metallib MUST sit next to the binary (the C++ kernel loader checks the
# binary's directory first).
#
# On a fresh clone, libs/mlx-swift (the vendored, patched mlx) is not present
# — it is bootstrapped automatically here, and the metallib is fetched from
# the official mlx wheel (matching version, see scripts/bootstrap-mlx.sh).
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -d libs/mlx-swift ]; then
    echo "==> first build: vendoring mlx-swift..."
    ../scripts/bootstrap-mlx.sh
fi

if [ ! -f vendor/mlx.metallib ]; then
    echo "==> fetching mlx.metallib (mlx 0.29.1, official wheel)..."
    mkdir -p vendor
    python3 -m pip install --quiet --target "$TMPDIR/mlxpy-dl" mlx==0.29.1
    cp "$TMPDIR/mlxpy-dl/mlx/lib/mlx.metallib" vendor/mlx.metallib
fi

swift build -c release
cp -f vendor/mlx.metallib .build/release/mlx.metallib
echo "built .build/release/idlegrid-provider (+ mlx.metallib)"
