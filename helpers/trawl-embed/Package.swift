// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "trawl-embed",
    platforms: [.macOS("26.0")],
    products: [
        .executable(name: "trawl-embed", targets: ["trawl-embed"]),
    ],
    dependencies: [
        .package(
            url: "https://github.com/apple/swift-protobuf.git",
            exact: "1.38.1"
        ),
        .package(
            url: "https://github.com/ml-explore/mlx-swift-lm.git",
            exact: "3.33.0"
        ),
        .package(
            url: "https://github.com/huggingface/swift-transformers.git",
            exact: "1.4.0"
        ),
    ],
    targets: [
        .executableTarget(
            name: "trawl-embed",
            dependencies: [
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
                .product(name: "MLXEmbedders", package: "mlx-swift-lm"),
                .product(name: "MLXLMCommon", package: "mlx-swift-lm"),
                .product(name: "Tokenizers", package: "swift-transformers"),
            ]
        ),
    ]
)
