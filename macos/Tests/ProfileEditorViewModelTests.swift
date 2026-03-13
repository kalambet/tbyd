import Testing
import Foundation
@testable import TBYDKit

// NOTE: ProfileEditorViewModel lives in the TBYDApp executable target, not TBYDKit.
// These tests exercise the TBYDKit types (Profile, ProfilePatch, ProfilePatch.build) and
// APIClient integration. The ViewModel-level tests below use a thin inline reimplementation
// of the same behaviour to keep the logic testable from the TBYDTests target.

// MARK: - Inline test-harness types (mirrors ProfileEditorView.swift)

/// Minimal identifiable item — mirrors the type defined in ProfileEditorView.swift.
struct TestEditableItem: Identifiable {
    let id: UUID
    var text: String
    init(id: UUID = UUID(), text: String) { self.id = id; self.text = text }
}

/// Minimal ViewModel that mirrors the real ProfileEditorViewModel logic using only TBYDKit types.
@MainActor
final class TestProfileEditorViewModel {
    var role: String = ""
    var tone: String?
    var detailLevel: String?
    var format: String?
    var opinions: [TestEditableItem] = []
    var preferences: [TestEditableItem] = []
    var errorMessage: String?
    var savedPatch: ProfilePatch?
    var httpCallCount: Int = 0

    private var originalProfile: Profile = Profile()

    func load(profile: Profile) {
        originalProfile = profile
        role = profile.identity.role ?? ""
        tone = profile.communication.tone
        detailLevel = profile.communication.detailLevel
        format = profile.communication.format
        opinions = profile.opinions.map { TestEditableItem(text: $0) }
        preferences = profile.preferences.map { TestEditableItem(text: $0) }
    }

    // Issue 3 fix: role is optional. Validation only blocks excessive-length items.
    func validate() -> String? {
        for item in opinions where item.text.count > 500 {
            return "Opinion items must be 500 characters or fewer."
        }
        for item in preferences where item.text.count > 500 {
            return "Preference items must be 500 characters or fewer."
        }
        return nil
    }

    func save(client: APIClient) async {
        if let err = validate() {
            errorMessage = err
            return
        }

        var current = originalProfile
        let trimmedRole = role.trimmingCharacters(in: .whitespacesAndNewlines)
        current.identity.role = trimmedRole.isEmpty ? nil : trimmedRole
        current.communication.tone = tone
        current.communication.detailLevel = detailLevel
        current.communication.format = format
        current.opinions = opinions.map(\.text)
        current.preferences = preferences.map(\.text)

        let patch = ProfilePatch.build(from: originalProfile, current: current)
        savedPatch = patch
        httpCallCount += 1

        do {
            try await client.patchProfile(patch)
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

// MARK: - Tests

@Suite("ProfileEditorViewModel", .serialized)
struct ProfileEditorViewModelTests {

    private func fullProfileJSON() -> Data {
        """
        {
            "identity": {
                "role": "Senior Engineer",
                "expertise": {"go": "expert"},
                "working_context": {
                    "current_projects": ["tbyd"],
                    "tech_stack": ["go"]
                }
            },
            "communication": {
                "tone": "direct",
                "detail_level": "concise",
                "format": "markdown"
            },
            "interests": {
                "primary": ["privacy"],
                "emerging": ["ai safety"]
            },
            "opinions": ["I value privacy"],
            "preferences": ["show code examples"],
            "language": "en"
        }
        """.data(using: .utf8)!
    }

    private func makeDecoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    @Test("load() populates all ViewModel fields from Profile")
    @MainActor
    func testLoad_PopulatesAllFields() throws {
        let profile = try makeDecoder().decode(Profile.self, from: fullProfileJSON())
        let vm = TestProfileEditorViewModel()
        vm.load(profile: profile)

        #expect(vm.role == "Senior Engineer")
        #expect(vm.tone == "direct")
        #expect(vm.detailLevel == "concise")
        #expect(vm.format == "markdown")
        #expect(vm.opinions.first?.text == "I value privacy")
        #expect(vm.preferences.first?.text == "show code examples")
    }

    @Test("save() sends PATCH with only the changed tone")
    @MainActor
    func testSave_SendsPATCH() async throws {
        let session = makeSession(for: ProfileEditorMock.self)
        var capturedBody: Data?

        ProfileEditorMock.handler = { request, body in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            if request.httpMethod == "PATCH" {
                capturedBody = body
            }
            return (response, "{}".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let profile = try makeDecoder().decode(Profile.self, from: fullProfileJSON())
        let vm = TestProfileEditorViewModel()
        vm.load(profile: profile)

        // Mutate only tone.
        vm.tone = "formal"

        await vm.save(client: client)

        #expect(vm.errorMessage == nil)
        #expect(vm.httpCallCount == 1)

        let body = try #require(capturedBody)
        let json = try JSONSerialization.jsonObject(with: body) as? [String: Any]

        // Flat dot-notation keys (Go server contract).
        #expect(json?["communication.tone"] as? String == "formal")

        // Unchanged sections must be absent.
        #expect(json?.keys.contains("identity.role") == false)
        #expect(json?.keys.contains("opinions") == false)

        // Nested keys must not appear.
        #expect(json?.keys.contains("communication") == false)
        #expect(json?.keys.contains("identity") == false)
    }

    @Test("save() sends JSON null when tone is cleared to nil")
    @MainActor
    func testSave_ClearToneSendsNull() async throws {
        let session = makeSession(for: ProfileEditorMock.self)
        var capturedBody: Data?

        ProfileEditorMock.handler = { request, body in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            if request.httpMethod == "PATCH" { capturedBody = body }
            return (response, "{}".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let profile = try makeDecoder().decode(Profile.self, from: fullProfileJSON())
        let vm = TestProfileEditorViewModel()
        vm.load(profile: profile)

        // Clear tone (was "direct").
        vm.tone = nil

        await vm.save(client: client)

        #expect(vm.errorMessage == nil)
        let body = try #require(capturedBody)
        let bodyString = String(data: body, encoding: .utf8) ?? ""
        #expect(bodyString.contains("tone"), "tone key must appear in body")
        #expect(bodyString.contains("null"), "tone must be JSON null when cleared")
    }

    @Test("save() with empty role makes HTTP call (role is optional)")
    @MainActor
    func testSave_EmptyRoleAllowed() async throws {
        let session = makeSession(for: ProfileEditorMock.self)
        ProfileEditorMock.handler = { request, _ in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, "{}".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let profile = try makeDecoder().decode(Profile.self, from: fullProfileJSON())
        let vm = TestProfileEditorViewModel()
        vm.load(profile: profile)

        vm.role = ""  // clearing role is permitted

        await vm.save(client: client)

        #expect(vm.errorMessage == nil, "empty role must not produce a validation error")
        #expect(vm.httpCallCount == 1, "HTTP call must proceed when role is empty")
    }

    @Test("save() returns validation error when opinion exceeds 500 characters")
    @MainActor
    func testValidation_LongOpinionBlocked() async throws {
        let session = makeSession(for: ProfileEditorMock.self)
        ProfileEditorMock.handler = { _, _ in
            Issue.record("HTTP request should not be made when validation fails")
            throw URLError(.unknown)
        }

        let client = APIClient(session: session, token: "test-token")
        let profile = try makeDecoder().decode(Profile.self, from: fullProfileJSON())
        let vm = TestProfileEditorViewModel()
        vm.load(profile: profile)

        let longText = String(repeating: "a", count: 501)
        vm.opinions = [TestEditableItem(text: longText)]

        await vm.save(client: client)

        #expect(vm.errorMessage != nil)
        #expect(vm.httpCallCount == 0)
    }

    @Test("Two identical opinion strings have distinct UUIDs after reorder")
    @MainActor
    func testReorderableList_StableIdentity() throws {
        let vm = TestProfileEditorViewModel()
        // Seed with two opinions that have the same text.
        vm.opinions = [
            TestEditableItem(id: UUID(), text: "A"),
            TestEditableItem(id: UUID(), text: "A"),
        ]

        let ids = vm.opinions.map(\.id)
        #expect(ids[0] != ids[1], "Duplicate text items must have distinct UUIDs")

        // Simulate reorder: swap positions.
        vm.opinions.swapAt(0, 1)

        // Both items must still exist with original distinct UUIDs (order swapped).
        #expect(vm.opinions.count == 2)
        #expect(vm.opinions[0].id == ids[1])
        #expect(vm.opinions[1].id == ids[0])
        #expect(vm.opinions[0].text == "A")
        #expect(vm.opinions[1].text == "A")
    }
}
