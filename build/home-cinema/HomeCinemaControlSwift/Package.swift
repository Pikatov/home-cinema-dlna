// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "HomeCinemaControlSwift",
    platforms: [
        .macOS(.v12)
    ],
    products: [
        .executable(
            name: "HomeCinemaControlSwift",
            targets: ["HomeCinemaControlSwift"]
        )
    ],
    targets: [
        .executableTarget(
            name: "HomeCinemaControlSwift"
        )
    ]
)
