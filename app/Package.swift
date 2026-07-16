// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "Trawl",
    platforms: [.macOS("26.0")],
    products: [
        .executable(name: "Trawl", targets: ["Trawl"]),
    ],
    dependencies: [
        .package(
            url: "https://github.com/apple/swift-protobuf.git",
            exact: "1.38.1"
        ),
        .package(
            url: "https://github.com/sparkle-project/Sparkle",
            exact: "2.9.4"
        ),
    ],
    targets: [
        .target(
            name: "TrawlClient",
            dependencies: [
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ]
        ),
        .target(name: "PermissionGuide"),
        .target(
            name: "TrawlCore",
            dependencies: ["PermissionGuide", "TrawlClient"]
        ),
        .executableTarget(
            name: "Trawl",
            dependencies: [
                "PermissionGuide",
                .product(name: "Sparkle", package: "Sparkle"),
                "TrawlClient",
                "TrawlCore",
            ],
            linkerSettings: [
                .unsafeFlags([
                    "-Xlinker", "-rpath",
                    "-Xlinker", "@executable_path/../Frameworks",
                ])
            ]
        ),
        .testTarget(
            name: "TrawlClientTests",
            dependencies: ["TrawlClient"]
        ),
        .testTarget(
            name: "PermissionGuideTests",
            dependencies: ["PermissionGuide"]
        ),
        .testTarget(
            name: "TrawlTests",
            dependencies: [
                "PermissionGuide",
                .product(name: "Sparkle", package: "Sparkle"),
                "TrawlClient",
                "TrawlCore",
                "Trawl",
            ],
            linkerSettings: [
                // SwiftPM links binary frameworks into executable tests but
                // does not copy Sparkle beside the generated .xctest bundle.
                .unsafeFlags([
                    "-Xlinker", "-rpath",
                    "-Xlinker", "@loader_path/../../../../../../artifacts/sparkle/Sparkle/Sparkle.xcframework/macos-arm64_x86_64",
                ])
            ]
        ),
    ]
)
