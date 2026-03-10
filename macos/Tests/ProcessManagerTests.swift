import Testing
import Foundation
@testable import TBYDKit

@Suite("ProcessManager")
struct ProcessManagerTests {

    @Test("Start spawns binary and transitions to running")
    @MainActor
    func startSpawnsBinary() throws {
        let manager = ProcessManager(binaryPath: "/bin/sleep", arguments: ["60"])
        try manager.start()
        #expect(manager.state == .running)
        #expect(manager.isRunning)
        manager.stop()
    }

    @Test("Stop sends SIGTERM and transitions to stopped state")
    @MainActor
    func stopTransitionsToStopped() throws {
        let manager = ProcessManager(binaryPath: "/bin/sleep", arguments: ["60"])
        try manager.start()
        #expect(manager.isRunning)
        manager.stop()
        if case .stopped = manager.state {
            // expected
        } else {
            Issue.record("Expected stopped state, got \(manager.state)")
        }
        #expect(!manager.isRunning)
    }

    @Test("Double start does not spawn second process")
    @MainActor
    func doubleStartNoOp() throws {
        let manager = ProcessManager(binaryPath: "/bin/sleep", arguments: ["60"])
        try manager.start()
        #expect(manager.state == .running)
        // Second start should be a no-op (guard in start()).
        try manager.start()
        #expect(manager.state == .running)
        manager.stop()
    }
}
