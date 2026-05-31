// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "HomeCinemaControlSwift",
    platforms: [
        // macOS 14 нужен для Observation framework (@Observable) и scenePhase
        // на macOS. Поддержка более ранних версий потребовала бы ObservableObject
        // и потеряла бы pause-animation-in-background.
        .macOS(.v14)
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
