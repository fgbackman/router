// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "RouterApp",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "RouterApp", path: "Sources/RouterApp")
    ]
)
