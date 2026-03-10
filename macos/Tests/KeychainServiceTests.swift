import Testing
import Foundation
@testable import TBYDKit

@Suite("KeychainService", .serialized)
struct KeychainServiceTests {
    /// Isolated service name so tests never touch production keychain entries.
    private static let testService = "tbyd-test"

    @Test("Loads API key from keychain round-trip")
    func loadsAPIKey() throws {
        try KeychainService.set(.openRouterAPIKey, value: "test-key-123", service: Self.testService)
        defer { try? KeychainService.delete(.openRouterAPIKey, service: Self.testService) }

        let loaded = try KeychainService.get(.openRouterAPIKey, service: Self.testService)
        #expect(loaded == "test-key-123")
    }

    @Test("Saves API key to keychain")
    func savesAPIKey() throws {
        defer { try? KeychainService.delete(.openRouterAPIKey, service: Self.testService) }

        try KeychainService.set(.openRouterAPIKey, value: "new-key-456", service: Self.testService)
        let loaded = try KeychainService.get(.openRouterAPIKey, service: Self.testService)
        #expect(loaded == "new-key-456")
    }

    @Test("Keychain returns nil for missing key")
    func keychainMissing() throws {
        try? KeychainService.delete(.openRouterAPIKey, service: Self.testService)
        let loaded = try KeychainService.get(.openRouterAPIKey, service: Self.testService)
        #expect(loaded == nil)
    }
}
