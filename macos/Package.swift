// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "tbyd",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "tbyd-menubar", targets: ["TBYDApp"]),
        .library(name: "ShareExtension", targets: ["ShareExtension"]),
    ],
    targets: [
        .executableTarget(
            name: "TBYDApp",
            dependencies: ["TBYDKit"],
            path: "Sources/App",
            exclude: ["Info.plist"]
        ),
        .target(
            name: "TBYDKit",
            path: "Sources/TBYDKit"
        ),
        .target(
            name: "ShareExtension",
            path: "Sources/ShareExtension",
            exclude: ["ShareExtensionInfo.plist"]
        ),
        .testTarget(
            name: "TBYDTests",
            dependencies: ["TBYDKit"],
            path: "Tests",
            exclude: ["ShareExtensionTests"]
        ),
        .testTarget(
            name: "ShareExtensionTests",
            dependencies: ["ShareExtension"],
            path: "Tests/ShareExtensionTests"
        ),
    ]
)
