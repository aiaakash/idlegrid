// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "idlegrid-provider",
    platforms: [.macOS(.v14)],
    dependencies: [
        // Apple's MLX Swift stack (in-process, Metal-backed LLM inference)
        .package(
            url: "https://github.com/ml-explore/mlx-swift-examples.git",
            .exactItem("2.29.1")
        ),
        .package(url: "https://github.com/apple/swift-crypto.git", "3.0.0"..<"4.0.0"),
        // VENDORED override of mlx-swift (same identity) — patched to use the
        // no-JIT Metal kernel path (precompiled mlx.metallib, colocated with
        // the binary). The remote package's JIT kernel path produces garbage
        // output on this toolchain; no-JIT matches the official Python wheels.
        .package(path: "libs/mlx-swift"),
    ],
    targets: [
        .executableTarget(
            name: "idlegrid-provider",
            dependencies: [
                .product(name: "MLXLMCommon", package: "mlx-swift-examples"),
                .product(name: "MLXLLM", package: "mlx-swift-examples"),
                .product(name: "Crypto", package: "swift-crypto"),
            ],
            path: "Sources/idlegrid-provider"
        )
    ]
)
