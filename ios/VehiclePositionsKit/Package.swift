// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "VehiclePositionsKit",
    platforms: [.iOS(.v18)],
    products: [.library(name: "VehiclePositionsKit", targets: ["VehiclePositionsKit"])],
    targets: [
        .target(name: "VehiclePositionsKit"),
        .testTarget(name: "VehiclePositionsKitTests", dependencies: ["VehiclePositionsKit"]),
    ],
    swiftLanguageModes: [.v6]
)
