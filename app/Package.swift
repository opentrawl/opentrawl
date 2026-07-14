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
            dependencies: ["PermissionGuide", "TrawlClient", "TrawlCore"]
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
            dependencies: ["PermissionGuide", "TrawlClient", "TrawlCore", "Trawl"]
        ),
    ]
)
