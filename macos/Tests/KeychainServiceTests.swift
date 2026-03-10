import Testing
import Foundation
@testable import TBYDKit

@Suite("KeychainService", .serialized)
struct KeychainServiceTests {

    @Test("Loads API key from keychain round-trip")
    func loadsAPIKey() throws {
        // Store a test key.
        try KeychainService.set(.openRouterAPIKey, value: "test-key-123")
        defer { try? KeychainService.delete(.openRouterAPIKey) }

        let loaded = try KeychainService.get(.openRouterAPIKey)
        #expect(loaded == "test-key-123")
    }

    @Test("Saves API key to keychain")
    func savesAPIKey() throws {
        defer { try? KeychainService.delete(.openRouterAPIKey) }

        try KeychainService.set(.openRouterAPIKey, value: "new-key-456")
        let loaded = try KeychainService.get(.openRouterAPIKey)
        #expect(loaded == "new-key-456")
    }

    @Test("Keychain returns nil for missing key")
    func keychainMissing() throws {
        // Ensure the key doesn't exist.
        try? KeychainService.delete(.openRouterAPIKey)
        let loaded = try KeychainService.get(.openRouterAPIKey)
        #expect(loaded == nil)
    }
}
