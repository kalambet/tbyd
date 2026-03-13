import Testing
import Foundation
@testable import TBYDKit

@Suite("Profile model", .serialized)
struct ProfileTests {

    // MARK: - Helpers

    private func makeDecoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    private func makeEncoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }

    private func makeFullProfile() -> Profile {
        var p = Profile()
        p.identity.role = "Senior Engineer"
        p.identity.expertise = ["go": "expert", "python": "intermediate"]
        var wc = Profile.WorkingContext()
        wc.currentProjects = ["tbyd", "oss-lib"]
        wc.teamSize = "5-10"
        wc.techStack = ["go", "swift"]
        p.identity.workingContext = wc
        p.communication.tone = "direct"
        p.communication.detailLevel = "concise"
        p.communication.format = "markdown"
        p.interests.primary = ["privacy", "distributed systems"]
        p.interests.emerging = ["ai safety"]
        p.opinions = ["I value privacy over convenience"]
        p.preferences = ["always show code examples", "avoid long preambles"]
        p.language = "en"
        p.cloudModelPreference = "gpt-4o"
        return p
    }

    // MARK: - Profile read model tests (unchanged: Profile uses nested JSON for GET)

    @Test("Profile round-trips through JSON without data loss")
    func testProfile_RoundTrip() throws {
        let original = makeFullProfile()
        let data = try makeEncoder().encode(original)
        let decoded = try makeDecoder().decode(Profile.self, from: data)
        #expect(decoded == original)
    }

    @Test("Profile decodes snake_case JSON keys into camelCase properties")
    func testProfile_DecodesSnakeCase() throws {
        let json = """
        {
            "communication": {
                "detail_level": "thorough",
                "tone": "formal"
            },
            "cloud_model_preference": "claude-3-5-sonnet",
            "opinions": [],
            "preferences": [],
            "identity": {"expertise": {}},
            "interests": {"primary": [], "emerging": []}
        }
        """.data(using: .utf8)!

        let profile = try makeDecoder().decode(Profile.self, from: json)
        #expect(profile.communication.detailLevel == "thorough")
        #expect(profile.communication.tone == "formal")
        #expect(profile.cloudModelPreference == "claude-3-5-sonnet")
    }

    @Test("Profile decodes expertise dictionary from JSON object")
    func testProfile_ExpertiseDict() throws {
        let json = """
        {
            "identity": {
                "expertise": {"go": "expert", "swift": "intermediate"}
            },
            "communication": {},
            "interests": {"primary": [], "emerging": []},
            "opinions": [],
            "preferences": []
        }
        """.data(using: .utf8)!

        let profile = try makeDecoder().decode(Profile.self, from: json)
        #expect(profile.identity.expertise["go"] == "expert")
        #expect(profile.identity.expertise["swift"] == "intermediate")
    }

    @Test("Profile decodes successfully when optional fields are absent")
    func testProfile_EmptyOptionals() throws {
        let json = """
        {
            "identity": {"expertise": {}},
            "communication": {},
            "interests": {"primary": [], "emerging": []},
            "opinions": [],
            "preferences": []
        }
        """.data(using: .utf8)!

        let profile = try makeDecoder().decode(Profile.self, from: json)
        #expect(profile.identity.role == nil)
        #expect(profile.communication.tone == nil)
        #expect(profile.communication.detailLevel == nil)
        #expect(profile.communication.format == nil)
        #expect(profile.cloudModelPreference == nil)
        #expect(profile.language == nil)
        #expect(profile.lastSynthesized == nil)
        #expect(profile.identity.workingContext == nil)
    }

    // MARK: - ProfilePatch flat key format tests (contract with Go server)

    /// Cross-domain contract test: every key in a ProfilePatch must be in the Go allowlist.
    /// The Go server's handlePatchProfile rejects unknown keys, so this test catches any
    /// encoding regression that would break the PATCH endpoint.
    @Test("ProfilePatch encodes only flat dot-notation keys matching the Go allowlist")
    func testProfilePatch_FlatKeyFormat() throws {
        var patch = ProfilePatch()
        var comm = ProfilePatch.CommunicationPatch()
        comm.tone = .some(.some("formal"))
        patch.communication = comm
        var id = ProfilePatch.IdentityPatch()
        id.role = .some(.some("engineer"))
        patch.identity = id
        patch.opinions = ["test"]

        let data = try JSONEncoder().encode(patch)
        let dict = try JSONSerialization.jsonObject(with: data) as! [String: Any]

        let goAllowlist: Set<String> = [
            "communication.tone", "communication.detail_level", "communication.format",
            "identity.role", "identity.expertise", "identity.working_context",
            "interests.primary", "interests.emerging",
            "opinions", "preferences", "language", "cloud_model_preference",
        ]

        for key in dict.keys {
            #expect(goAllowlist.contains(key), "Key '\(key)' not in Go allowlist")
        }

        // Spot-check expected keys are present with correct values.
        #expect(dict["communication.tone"] as? String == "formal")
        #expect(dict["identity.role"] as? String == "engineer")
        let opinions = dict["opinions"] as? [String]
        #expect(opinions == ["test"])

        // Nested keys must NOT appear.
        #expect(dict["communication"] == nil, "nested 'communication' key must be absent")
        #expect(dict["identity"] == nil, "nested 'identity' key must be absent")
    }

    @Test("ProfilePatch.build produces only the changed communication tone key")
    func testProfilePatch_OnlyChangedSectionSent() throws {
        var original = Profile()
        original.communication.tone = "direct"

        var current = original
        current.communication.tone = "formal"

        let patch = ProfilePatch.build(from: original, current: current)
        let data = try JSONEncoder().encode(patch)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]

        // Flat key present with correct value.
        #expect(json?["communication.tone"] as? String == "formal")

        // Other flat keys absent.
        #expect(json?.keys.contains("communication.detail_level") == false)
        #expect(json?.keys.contains("communication.format") == false)
        #expect(json?.keys.contains("identity.role") == false)
        #expect(json?.keys.contains("opinions") == false)
        #expect(json?.keys.contains("preferences") == false)

        // Nested keys must not appear.
        #expect(json?.keys.contains("communication") == false)
        #expect(json?.keys.contains("identity") == false)
    }

    @Test("ProfilePatch with empty opinions array encodes as an explicit empty array")
    func testProfilePatch_ArrayClearable() throws {
        var patch = ProfilePatch()
        patch.opinions = []

        let data = try JSONEncoder().encode(patch)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]

        #expect(json?.keys.contains("opinions") == true, "opinions key must be present")
        let opinions = json?["opinions"] as? [Any]
        #expect(opinions?.isEmpty == true, "opinions array must be empty")
    }

    @Test("ProfilePatch with only communication set omits identity keys entirely")
    func testProfilePatch_NilSectionOmitted() throws {
        var patch = ProfilePatch()
        var cp = ProfilePatch.CommunicationPatch()
        cp.tone = .some(.some("friendly"))
        patch.communication = cp

        let data = try JSONEncoder().encode(patch)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]

        // No identity keys at all.
        #expect(json?.keys.contains("identity.role") == false)
        #expect(json?.keys.contains("identity.expertise") == false)
        #expect(json?.keys.contains("identity.working_context") == false)
        // No nested key either.
        #expect(json?.keys.contains("identity") == false)

        // Communication tone present.
        #expect(json?["communication.tone"] as? String == "friendly")
    }

    // MARK: - Null encoding tests

    @Test("Clearing tone to nil encodes as flat key with JSON null value")
    func testProfilePatch_ClearToneEncodesNull() throws {
        var original = Profile()
        original.communication.tone = "direct"

        var current = original
        current.communication.tone = nil

        let patch = ProfilePatch.build(from: original, current: current)
        let data = try JSONEncoder().encode(patch)
        let bodyString = String(data: data, encoding: .utf8) ?? ""

        #expect(bodyString.contains("\"communication.tone\""),
                "communication.tone key must appear in the patch body")
        #expect(bodyString.contains("null"), "value must be JSON null when cleared")
        #expect(!bodyString.contains("\"direct\""), "original value must not appear")
    }

    @Test("Clearing detail_level to nil encodes as flat key with JSON null value")
    func testProfilePatch_ClearDetailLevelEncodesNull() throws {
        var original = Profile()
        original.communication.detailLevel = "concise"

        var current = original
        current.communication.detailLevel = nil

        let patch = ProfilePatch.build(from: original, current: current)
        let data = try JSONEncoder().encode(patch)
        let bodyString = String(data: data, encoding: .utf8) ?? ""

        #expect(bodyString.contains("communication.detail_level"),
                "communication.detail_level key must be present")
        #expect(bodyString.contains("null"), "value must be JSON null")
    }

    @Test("Clearing role to nil encodes as flat key with JSON null value")
    func testProfilePatch_ClearRoleEncodesNull() throws {
        var original = Profile()
        original.identity.role = "Engineer"

        var current = original
        current.identity.role = nil

        let patch = ProfilePatch.build(from: original, current: current)
        let data = try JSONEncoder().encode(patch)
        let bodyString = String(data: data, encoding: .utf8) ?? ""

        #expect(bodyString.contains("\"identity.role\""),
                "identity.role key must appear in the patch body")
        #expect(bodyString.contains("null"), "value must be JSON null when cleared")
        #expect(!bodyString.contains("\"Engineer\""), "original value must not appear")
    }

    @Test("Tone unchanged between original and current produces no communication keys")
    func testProfilePatch_UnchangedToneOmitted() throws {
        var original = Profile()
        original.communication.tone = "direct"
        let current = original

        let patch = ProfilePatch.build(from: original, current: current)
        let data = try JSONEncoder().encode(patch)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]

        #expect(json?.keys.contains("communication.tone") == false)
        #expect(json?.keys.contains("communication.detail_level") == false)
        #expect(json?.keys.contains("communication.format") == false)
        #expect(json?.keys.contains("communication") == false)
    }
}
