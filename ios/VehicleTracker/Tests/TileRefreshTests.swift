import Foundation
import Testing
@testable import VehicleTracker

/// The vehicle screen's reload of tiles it already has. The refresh banner
/// shows a spinner for Try again while `isRunning` is set, so it must be set
/// for as long as the reload runs and only then, and a second tap must not
/// start a second load.
@Suite struct TileRefreshTests {
    final class Reloads {
        var count = 0
    }

    @Test func itIsRunningForAsLongAsTheReloadRuns() async {
        let refresh = TileRefresh()
        var runningDuringTheReload = false
        await refresh.run { runningDuringTheReload = refresh.isRunning }
        #expect(runningDuringTheReload)
        #expect(!refresh.isRunning)
    }

    @Test func aSecondTapWhileItRunsLoadsNothing() async {
        let refresh = TileRefresh()
        let reloads = Reloads()
        let (released, release) = AsyncStream.makeStream(of: Void.self)
        let first = Task {
            await refresh.run {
                reloads.count += 1
                for await _ in released { break }
            }
        }
        #expect(await eventually { refresh.isRunning })

        await refresh.run { reloads.count += 1 }
        #expect(reloads.count == 1)
        #expect(refresh.isRunning, "the refused tap ended the first reload's spinner")

        release.yield(())
        await first.value
        // A retry that failed brings Try again back, and that tap reloads.
        await refresh.run { reloads.count += 1 }
        #expect(reloads.count == 2)
    }
}
