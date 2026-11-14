// swift-tools-version: 6.1
import PackageDescription

let package = Package(
    name: "ShiojiPermissionFlow",
    platforms: [.macOS(.v13)],
    products: [.library(name: "ShiojiPermissionFlow", type: .dynamic, targets: ["ShiojiPermissionFlow"])],
    dependencies: [
        .package(url: "https://github.com/jaywcjlove/PermissionFlow.git", revision: "65434c9ae04dba97cf52c1dffbe3e4e62b547c82")
    ],
    targets: [
        .target(name: "ShiojiPermissionFlow", dependencies: [.product(name: "PermissionFlow", package: "PermissionFlow")]),
        .testTarget(name: "ShiojiPermissionFlowTests", dependencies: ["ShiojiPermissionFlow"])
    ]
)
