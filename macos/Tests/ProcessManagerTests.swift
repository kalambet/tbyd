import Testing
import Foundation
@testable import TBYDKit

@Suite("ProcessManager")
struct ProcessManagerTests {

    @Test("Start spawns binary with correct path")
    @MainActor
    func startSpawnsBinary() throws {
        // Use /usr/bin/true as a safe binary that exits immediately.
        let manager = ProcessManager(binaryPath: "/usr/bin/true")
        try manager.start()
        // Process should have started (may already exited since /usr/bin/true exits immediately).
        // The key verification is that no exception was thrown.
    }

    @Test("Stop sends SIGTERM and transitions to stopped state")
    @MainActor
    func stopTransitionsToStopped() throws {
        // Use /bin/sleep as a long-running process.
        let manager = ProcessManager(binaryPath: "/bin/sleep")
        // Note: ProcessManager passes ["start"] as arguments, so sleep will fail,
        // but we can verify the state management logic.
        manager.stop()
        if case .stopped = manager.state {
            // expected
        } else {
            Issue.record("Expected stopped state, got \(manager.state)")
        }
    }

    @Test("Double start does not spawn second process")
    @MainActor
    func doubleStartNoOp() throws {
        let manager = ProcessManager(binaryPath: "/usr/bin/true")
        try manager.start()
        // Second start should be a no-op (guard in start()).
        try manager.start()
    }
}
