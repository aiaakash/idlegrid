#!/bin/bash
# Compiles MLX's Metal kernels into mlx.metallib and colocates it with the
# provider binary — mirroring mlx's kernels/CMakeLists.txt. Without this the
# C++ layer fails at runtime: "Failed to load the default metallib".
set -euo pipefail
cd "$(dirname "$0")"

CHECKOUT=".build/checkouts/mlx-swift/Source/Cmlx/mlx"
KERNELS="$CHECKOUT/mlx/backend/metal/kernels"
OUT=".build/metallib"
mkdir -p "$OUT"

# metal_3_1 includes for newer toolchains (MLX_METAL_VERSION >= 310)
VERSION_INCLUDES="$KERNELS/metal_3_1"
METAL_FLAGS=(-Wall -Wextra -fno-fast-math -Wno-c++17-extensions)

build_kernel() {
    local stem="$1"; shift
    local src="$KERNELS/$stem.metal"
    [ -f "$src" ] || { echo "missing kernel: $src"; exit 1; }
    if [ ! -f "$OUT/$stem.air" ] || [ "$src" -nt "$OUT/$stem.air" ]; then
        xcrun -sdk macosx metal "${METAL_FLAGS[@]}" -c "$src" \
            -I"$CHECKOUT" -I"$VERSION_INCLUDES" -o "$OUT/$stem.air"
        echo "built $stem.air"
    fi
}

build_kernel arg_reduce
build_kernel conv steel/conv/params.h
build_kernel gemv steel/utils.h
build_kernel layer_norm
build_kernel random
build_kernel rms_norm
build_kernel rope
build_kernel scaled_dot_product_attention sdpa_vector.h
build_kernel fence
build_kernel arange arange.h
build_kernel binary binary.h binary_ops.h
build_kernel binary_two binary_two.h
build_kernel copy copy.h
build_kernel fft fft.h fft/radix.h fft/readwrite.h
build_kernel reduce atomic.h reduction/ops.h reduction/reduce_init.h reduction/reduce_all.h reduction/reduce_col.h reduction/reduce_row.h
build_kernel quantized quantized.h quantized_utils.h
build_kernel fp4_quantized fp4_quantized.h quantized_utils.h
build_kernel scan scan.h
build_kernel softmax softmax.h
build_kernel logsumexp logsumexp.h
build_kernel sort sort.h
build_kernel ternary ternary.h ternary_ops.h
build_kernel unary unary.h unary_ops.h
build_kernel steel/conv/kernels/steel_conv
build_kernel steel/conv/kernels/steel_conv_general
build_kernel steel/gemm/kernels/steel_gemm_fused
build_kernel steel/gemm/kernels/steel_gemm_gather
build_kernel steel/gemm/kernels/steel_gemm_masked
build_kernel steel/gemm/kernels/steel_gemm_splitk
build_kernel steel/gemm/kernels/steel_gemm_segmented
build_kernel gemv_masked steel/utils.h

xcrun -sdk macosx metallib "$OUT"/*.air -o "$OUT/mlx.metallib"

# Colocate with every built binary flavor (the C++ loader checks the
# binary's own directory first).
for bin_dir in .build/release .build/debug; do
    if [ -d "$bin_dir" ]; then
        cp -f "$OUT/mlx.metallib" "$bin_dir/mlx.metallib"
    fi
done
echo "mlx.metallib built and colocated ($(du -h "$OUT/mlx.metallib" | cut -f1))"
