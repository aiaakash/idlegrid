#!/bin/bash
# Vendors Apple's mlx-swift into libs/mlx-swift and applies our one-line fix
# (no-JIT Metal kernels). Needed on fresh clones before building the provider.
# Idempotent: skips if libs/mlx-swift already exists.
set -euo pipefail
cd "$(dirname "$0")/../provider-swift"

DEST="libs/mlx-swift"
if [ -d "$DEST" ]; then
    echo "mlx-swift already vendored at $DEST"
    exit 0
fi

# Pinned revision of ml-explore/mlx-swift that the metallib (mlx 0.29.1) and
# the no-JIT patch were validated against. Bump deliberately, with a new
# metallib, and re-test generation quality.
REV="072b684acaae80b6a463abab3a103732f33774bf"
echo "vendoring mlx-swift @ $REV"

git clone --filter=blob:none https://github.com/ml-explore/mlx-swift.git "$DEST"
git -C "$DEST" checkout -q "$REV"
# Source/Cmlx/mlx is a git SUBMODULE (the actual MLX C++ source) — a plain
# clone leaves it empty. Materialize it at the revision's pinned pointer:
git -C "$DEST" submodule update --init --recursive --depth 1
chmod -R u+w "$DEST"
rm -rf "$DEST/.git" "$DEST/Source/Cmlx/mlx/.git"

# Verify the critical files actually exist before declaring success.
test -f "$DEST/Package.swift" || { echo "vendoring failed: Package.swift missing"; exit 1; }
test -f "$DEST/Source/Cmlx/mlx/mlx/version.h" || { echo "vendoring failed: mlx submodule not materialized"; exit 1; }
echo "mlx source verified ($(find "$DEST/Source/Cmlx/mlx" -type f | wc -l | tr -d ' ') files)"

# THE PATCH: no-JIT Metal kernels. The JIT path produces garbage generations
# on CLT 26.6-era toolchains; no-JIT + a matching mlx.metallib is correct
# (it is what the official Python wheels use).
python3 - <<'EOF'
p = "libs/mlx-swift/Package.swift"
src = open(p).read()
old = '                "mlx/mlx/backend/metal/nojit_kernels.cpp",\n'
new = '                "mlx/mlx/backend/metal/jit_kernels.cpp",\n'
if old in src:
    src = src.replace(old, new, 1)
    open(p, "w").write(src)
    print("patched: no-JIT kernels enabled, JIT excluded")
elif new in src:
    print("already patched")
else:
    print("WARNING: expected patch site not found — check Package.swift manually")
    raise SystemExit(1)
EOF
echo "done"
