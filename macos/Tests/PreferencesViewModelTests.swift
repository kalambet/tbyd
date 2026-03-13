import Testing
import Foundation
@testable import TBYDKit

@MainActor
@Suite("PreferencesViewModel Tests", .serialized)
struct PreferencesViewModelTests {

    // MARK: - Mock implementations

    private final class MockKeychainService: KeychainServiceProtocol, @unchecked Sendable {
        var storage: [KeychainService.Account: String] = [:]

        func get(_ account: KeychainService.Account) throws -> String? {
            storage[account]
        }

        func set(_ account: KeychainService.Account, value: String) throws {
            storage[account] = value
        }
    }

    private final class MockLaunchAgentManager: LaunchAgentManagerProtocol, @unchecked Sendable {
        var isEnabled: Bool = false
        var setEnabledCalls: [Bool] = []

        func setEnabled(_ enabled: Bool) throws {
            setEnabledCalls.append(enabled)
            isEnabled = enabled
        }
    }

    private final class MockConfigService: ConfigServiceProtocol, @unchecked Sendable {
        var values: [String: String] = [:]

        func readValues() async -> [String: String] { values }
        func setValue(_ key: String, value: String) async throws { values[key] = value }
    }

    // MARK: - Tests

    @Test("saveAPIKey writes the key to keychain")
    func testSaveAPIKey() throws {
        let mock = MockKeychainService()
        let vm = PreferencesViewModel(keychain: mock)
        vm.apiKey = "sk-test-1234"
        vm.saveAPIKey()
        #expect(mock.storage[.openRouterAPIKey] == "sk-test-1234")
    }

    @Test("load reads existing API key from keychain")
    func testLoadAPIKey() async throws {
        let mockKeychain = MockKeychainService()
        mockKeychain.storage[.openRouterAPIKey] = "pre-existing-key"

        PreferencesMock.handler = { request, _ in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            // Return an empty models list
            let body = #"{"data":[]}"#.data(using: .utf8)!
            return (response, body)
        }

        let session = makeSession(for: PreferencesMock.self)
        let client = APIClient(session: session, token: "test-token")
        let vm = PreferencesViewModel(keychain: mockKeychain, launchAgent: MockLaunchAgentManager(), configService: MockConfigService())
        await vm.load(client: client)

        #expect(vm.apiKey == "pre-existing-key")
    }

    @Test("setSaveInteractions sends PATCH /profile request")
    func testSetSaveInteractions() async throws {
        // Capture state via a class so the closure and test body share the same reference.
        final class Capture: @unchecked Sendable {
            var request: URLRequest?
            var body: Data?
        }
        let capture = Capture()

        PreferencesMock.handler = { request, body in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            if request.httpMethod == "GET" {
                return (response, #"{"data":[]}"#.data(using: .utf8)!)
            }
            capture.request = request
            capture.body = body
            return (response, "{}".data(using: .utf8)!)
        }

        let session = makeSession(for: PreferencesMock.self)
        let client = APIClient(session: session, token: "test-token")
        let vm = PreferencesViewModel(keychain: MockKeychainService(), launchAgent: MockLaunchAgentManager(), configService: MockConfigService())
        await vm.load(client: client)
        await vm.setSaveInteractions(true)

        #expect(capture.request != nil)
        #expect(capture.request?.httpMethod == "PATCH")
        #expect(capture.request?.url?.path == "/profile")

        // Verify the body contains save_interactions: true
        if let body = capture.body,
           let json = try? JSONSerialization.jsonObject(with: body) as? [String: Any] {
            #expect(json["save_interactions"] as? Bool == true)
        } else {
            Issue.record("PATCH request body was missing or not valid JSON")
        }
    }

    @Test("setAutoStart enables LaunchAgentManager")
    func testSetAutoStart_enable() throws {
        let mockAgent = MockLaunchAgentManager()
        let vm = PreferencesViewModel(launchAgent: mockAgent)
        vm.setAutoStart(true)
        #expect(mockAgent.isEnabled == true)
        #expect(mockAgent.setEnabledCalls == [true])
    }

    @Test("setAutoStart disables LaunchAgentManager")
    func testSetAutoStart_disable() throws {
        let mockAgent = MockLaunchAgentManager()
        mockAgent.isEnabled = true
        let vm = PreferencesViewModel(launchAgent: mockAgent)
        vm.setAutoStart(false)
        #expect(mockAgent.isEnabled == false)
        #expect(mockAgent.setEnabledCalls == [false])
    }
}
