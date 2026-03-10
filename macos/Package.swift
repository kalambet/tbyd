// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "tbyd",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "tbyd-menubar", targets: ["TBYDApp"]),
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
        .testTarget(
            name: "TBYDTests",
            dependencies: ["TBYDKit"],
            path: "Tests"
        ),
    ]
)
