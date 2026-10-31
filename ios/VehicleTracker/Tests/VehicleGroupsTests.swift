import Foundation
import Testing
@testable import VehicleTracker

/// A port of the vehicle cases in Android's `ViewModelsTest`: the same three
/// vehicles, in the same order, under the same sections.
@Suite struct VehicleGroupsTests {
    let nightOwl = Vehicle(id: "bus-3", label: "Night Owl")
    let airport = Vehicle(id: "van-2", label: "Airport Shuttle")
    let downtown = Vehicle(id: "bus-1", label: "Downtown Express")
    var three: [Vehicle] { [nightOwl, airport, downtown] }

    func ids(_ groups: [VehicleGroup]) -> [String] { groups.flatMap { $0.tiles.map(\.id) } }

    /// Each section with its ids, as one line per section.
    func layout(_ groups: [VehicleGroup]) -> [String] {
        groups.map { "\($0.section): \($0.tiles.map(\.id).joined(separator: " "))" }
    }

    @Test func searchFiltersByIDAndLabel() {
        #expect(ids(VehicleGroups.of(three, query: "bus", favorites: [], recents: [])) == ["bus-1", "bus-3"])
        #expect(ids(VehicleGroups.of(three, query: "airport", favorites: [], recents: [])) == ["van-2"])
    }

    @Test func searchIsCaseInsensitive() {
        #expect(ids(VehicleGroups.of(three, query: "BUS", favorites: [], recents: [])) == ["bus-1", "bus-3"])
    }

    @Test func anEmptyQueryShowsEveryVehicleByLabel() {
        #expect(ids(VehicleGroups.of(three, query: "", favorites: [], recents: [])) == ["van-2", "bus-1", "bus-3"])
    }

    @Test func noMatchLeavesNoSection() {
        #expect(VehicleGroups.of(three, query: "tram", favorites: [], recents: []).isEmpty)
    }

    @Test func favoritesSortFirst() {
        // "Night Owl" sorts last by label, so only the star can put bus-3 in front.
        #expect(ids(VehicleGroups.of(three, query: "", favorites: ["bus-3"], recents: [])) == ["bus-3", "van-2", "bus-1"])
    }

    @Test func recentsOrderMostRecentFirst() {
        // van-2 was used last, so it leads bus-3 even though its label sorts
        // first anyway; bus-1 has never been used and falls to the bottom.
        #expect(ids(VehicleGroups.of(three, query: "", favorites: [], recents: ["van-2", "bus-3"])) == ["van-2", "bus-3", "bus-1"])
        #expect(ids(VehicleGroups.of(three, query: "", favorites: [], recents: ["bus-3", "van-2"])) == ["bus-3", "van-2", "bus-1"])
    }

    @Test func aFavoriteOutranksAMoreRecentlyUsedVehicle() {
        #expect(ids(VehicleGroups.of(three, query: "", favorites: ["bus-3"], recents: ["van-2"])) == ["bus-3", "van-2", "bus-1"])
    }

    @Test func vehiclesSitUnderFavoritesThenRecentThenAllEachInOneSection() {
        // bus-3 is both a favourite and the latest used: it is listed once, as a favourite.
        let groups = VehicleGroups.of(three, query: "", favorites: ["bus-3"], recents: ["bus-3", "van-2"])
        #expect(layout(groups) == ["favorites: bus-3", "recent: van-2", "all: bus-1"])
    }

    @Test func aSectionWithNoVehiclesIsLeftOut() {
        #expect(layout(VehicleGroups.of(three, query: "", favorites: [], recents: [])) == ["all: van-2 bus-1 bus-3"])
        #expect(layout(VehicleGroups.of(three, query: "", favorites: ["van-2", "bus-1", "bus-3"], recents: [])) == ["favorites: van-2 bus-1 bus-3"])
    }

    @Test func whileSearchingEveryMatchIsUnderMatchesInTheUsualOrder() {
        #expect(layout(VehicleGroups.of(three, query: "bus", favorites: ["bus-3"], recents: [])) == ["matches: bus-3 bus-1"])
    }

    @Test func aRecentVehicleNoLongerAssignedIsNotShown() {
        // Its id stays stored, and comes back if the vehicle is assigned again.
        #expect(layout(VehicleGroups.of(three, query: "", favorites: [], recents: ["gone"])) == ["all: van-2 bus-1 bus-3"])
    }

    @Test func aTileIsStarredWhenItIsAFavorite() {
        let tiles = VehicleGroups.of(three, query: "", favorites: ["van-2"], recents: []).flatMap(\.tiles)
        #expect(tiles.map { "\($0.id) \($0.favorite)" } == ["van-2 true", "bus-1 false", "bus-3 false"])
    }

    @Test func aTileShowsTheLabelUnderTheFleetNumberOnlyWhenItAddsSomething() {
        let vehicles = [Vehicle(id: "4317", label: "Bus 4317"), Vehicle(id: "4318", label: "4318"), Vehicle(id: "4319", label: " ")]
        let tiles = VehicleGroups.of(vehicles, query: "", favorites: [], recents: []).flatMap(\.tiles)
        #expect(tiles.map { "\($0.id) \($0.label ?? "-")" } == ["4319 -", "4318 -", "4317 Bus 4317"])
    }
}
