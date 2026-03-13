import Foundation

// MARK: - Profile (read/display model)

/// Typed representation of the tbyd user profile returned by GET /profile.
/// Non-optional root properties eliminate nil-checking throughout the UI.
/// Optional leaf fields reflect fields the user may not have set yet.
public struct Profile: Codable, Equatable, Sendable {

    // MARK: Nested types

    public struct Communication: Codable, Equatable, Sendable {
        /// "direct" | "friendly" | "formal"
        public var tone: String?
        /// "concise" | "balanced" | "thorough"
        public var detailLevel: String?
        /// "prose" | "markdown" | "structured"
        public var format: String?

        public init() {}

        enum CodingKeys: String, CodingKey {
            case tone
            case detailLevel = "detail_level"
            case format
        }
    }

    public struct WorkingContext: Codable, Equatable, Sendable {
        public var currentProjects: [String]
        public var teamSize: String?
        public var techStack: [String]

        public init() {
            currentProjects = []
            techStack = []
        }

        enum CodingKeys: String, CodingKey {
            case currentProjects = "current_projects"
            case teamSize = "team_size"
            case techStack = "tech_stack"
        }
    }

    public struct Identity: Codable, Equatable, Sendable {
        public var role: String?
        /// skill → level, e.g. {"go": "expert", "python": "intermediate"}
        public var expertise: [String: String]
        public var workingContext: WorkingContext?

        public init() {
            expertise = [:]
        }

        enum CodingKeys: String, CodingKey {
            case role
            case expertise
            case workingContext = "working_context"
        }
    }

    public struct Interests: Codable, Equatable, Sendable {
        public var primary: [String]
        public var emerging: [String]

        public init() {
            primary = []
            emerging = []
        }
    }

    // MARK: Root properties

    public var identity: Identity
    public var communication: Communication
    public var interests: Interests
    public var opinions: [String]
    public var preferences: [String]
    public var language: String?
    public var cloudModelPreference: String?
    public var lastSynthesized: Date?

    public init() {
        identity = Identity()
        communication = Communication()
        interests = Interests()
        opinions = []
        preferences = []
    }

    enum CodingKeys: String, CodingKey {
        case identity
        case communication
        case interests
        case opinions
        case preferences
        case language
        case cloudModelPreference = "cloud_model_preference"
        case lastSynthesized = "last_synthesized"
    }
}

// MARK: - NullableField

/// Encodes a value that may be intentionally set to JSON null (to clear a field on the server),
/// as distinct from a field that is absent (omitted from the patch body entirely).
///
/// - `nil`             → key omitted from JSON  (don't touch this field)
/// - `.some(nil)`      → key encoded as `null`  (clear this field on the server)
/// - `.some(.some(v))` → key encoded as `"v"`   (set this field to a new value)
public typealias NullableField<T: Encodable & Sendable> = Optional<Optional<T>>

// MARK: - DynamicCodingKey

/// A `CodingKey` that accepts any runtime string, used to produce flat dot-notation keys
/// in `ProfilePatch.encode(to:)`.
struct DynamicCodingKey: CodingKey {
    var stringValue: String
    var intValue: Int? { nil }
    init(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { nil }
}

// MARK: - KeyedEncodingContainer helpers

extension KeyedEncodingContainer where K == DynamicCodingKey {
    /// Encodes a `NullableField<T>` under an arbitrary string key:
    /// - outer `nil`          → omit key
    /// - outer `.some(nil)`   → encode JSON null
    /// - outer `.some(.some(v))` → encode value
    mutating func encodeNullable<T: Encodable>(
        _ value: NullableField<T>,
        forKey rawKey: String
    ) throws {
        let key = DynamicCodingKey(stringValue: rawKey)
        switch value {
        case .none:
            break
        case .some(.none):
            try encodeNil(forKey: key)
        case .some(.some(let wrapped)):
            try encode(wrapped, forKey: key)
        }
    }

    /// Encodes an optional value under an arbitrary string key, omitting when nil.
    mutating func encodeIfPresent<T: Encodable>(
        _ value: T?,
        forKey rawKey: String
    ) throws {
        if let value {
            try encode(value, forKey: DynamicCodingKey(stringValue: rawKey))
        }
    }
}

// MARK: - ProfilePatch (write-only model for PATCH /profile)

/// Write-only model for PATCH /profile.
///
/// Encodes to **flat dot-notation keys** matching the Go server's allowlist:
/// `"communication.tone"`, `"identity.role"`, `"opinions"`, etc.
///
/// Absent fields (nil outer) are omitted — server leaves them unchanged.
/// Fields cleared to nil encode as JSON `null` via `NullableField` — server deletes them.
/// Non-nil arrays (even empty) are sent as-is, distinguishing "clear" from "absent".
public struct ProfilePatch: Encodable, Sendable {

    // MARK: Nested field containers
    // These are plain data bags. All encoding is handled by ProfilePatch.encode(to:).

    public struct CommunicationPatch: Sendable {
        /// Outer nil = omit. Inner nil = send JSON null (clear on server).
        public var tone: NullableField<String> = nil
        public var detailLevel: NullableField<String> = nil
        public var format: NullableField<String> = nil
        public init() {}
    }

    public struct IdentityPatch: Sendable {
        public var role: NullableField<String> = nil
        public var expertise: [String: String]?
        public var workingContext: Profile.WorkingContext?
        public init() {}
    }

    public struct InterestsPatch: Sendable {
        public var primary: [String]?
        public var emerging: [String]?
        public init() {}
    }

    // MARK: Root patch fields

    public var identity: IdentityPatch?
    public var communication: CommunicationPatch?
    public var interests: InterestsPatch?
    public var opinions: [String]?
    public var preferences: [String]?
    public var language: String?
    public var cloudModelPreference: String?

    public init() {}

    // MARK: Flat dot-notation encoding

    /// Encodes the patch as a flat JSON object with dot-notation keys, matching the Go
    /// server's `handlePatchProfile` allowlist exactly.
    ///
    /// Go allowlist:
    ///   communication.tone, communication.detail_level, communication.format,
    ///   identity.role, identity.expertise, identity.working_context,
    ///   interests.primary, interests.emerging,
    ///   opinions, preferences, language, cloud_model_preference
    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: DynamicCodingKey.self)

        // Communication fields
        if let comm = communication {
            try c.encodeNullable(comm.tone, forKey: "communication.tone")
            try c.encodeNullable(comm.detailLevel, forKey: "communication.detail_level")
            try c.encodeNullable(comm.format, forKey: "communication.format")
        }

        // Identity fields
        if let id = identity {
            try c.encodeNullable(id.role, forKey: "identity.role")
            try c.encodeIfPresent(id.expertise, forKey: "identity.expertise")
            try c.encodeIfPresent(id.workingContext, forKey: "identity.working_context")
        }

        // Interests fields
        if let interests = interests {
            try c.encodeIfPresent(interests.primary, forKey: "interests.primary")
            try c.encodeIfPresent(interests.emerging, forKey: "interests.emerging")
        }

        // Root flat fields
        try c.encodeIfPresent(opinions, forKey: "opinions")
        try c.encodeIfPresent(preferences, forKey: "preferences")
        try c.encodeIfPresent(language, forKey: "language")
        try c.encodeIfPresent(cloudModelPreference, forKey: "cloud_model_preference")
    }
}

// MARK: - ProfilePatch builder

extension ProfilePatch {

    /// Builds a `ProfilePatch` by diffing `current` state against the `original` snapshot.
    /// Only sections with at least one changed field are populated on the patch.
    /// Fields changed to nil produce JSON `null` (via `NullableField`) rather than being
    /// silently omitted, so the server correctly clears them.
    public static func build(from original: Profile, current: Profile) -> ProfilePatch {
        var patch = ProfilePatch()

        // Communication section
        if current.communication != original.communication {
            var cp = CommunicationPatch()
            if current.communication.tone != original.communication.tone {
                cp.tone = .some(current.communication.tone)
            }
            if current.communication.detailLevel != original.communication.detailLevel {
                cp.detailLevel = .some(current.communication.detailLevel)
            }
            if current.communication.format != original.communication.format {
                cp.format = .some(current.communication.format)
            }
            patch.communication = cp
        }

        // Identity section
        if current.identity != original.identity {
            var ip = IdentityPatch()
            if current.identity.role != original.identity.role {
                ip.role = .some(current.identity.role)
            }
            if current.identity.expertise != original.identity.expertise {
                ip.expertise = current.identity.expertise
            }
            if current.identity.workingContext != original.identity.workingContext {
                ip.workingContext = current.identity.workingContext
            }
            patch.identity = ip
        }

        // Interests section
        if current.interests != original.interests {
            var interestsPatch = InterestsPatch()
            if current.interests.primary != original.interests.primary {
                interestsPatch.primary = current.interests.primary
            }
            if current.interests.emerging != original.interests.emerging {
                interestsPatch.emerging = current.interests.emerging
            }
            patch.interests = interestsPatch
        }

        // Flat array fields
        if current.opinions != original.opinions {
            patch.opinions = current.opinions
        }
        if current.preferences != original.preferences {
            patch.preferences = current.preferences
        }

        // Flat scalar fields
        if current.language != original.language {
            patch.language = current.language
        }
        if current.cloudModelPreference != original.cloudModelPreference {
            patch.cloudModelPreference = current.cloudModelPreference
        }

        return patch
    }
}

// MARK: - Legacy free function (deprecated)

/// Deprecated. Prefer `ProfilePatch.build(from:current:)`.
@available(*, deprecated, renamed: "ProfilePatch.build(from:current:)")
public func buildPatch(from original: Profile, current: Profile) -> ProfilePatch {
    ProfilePatch.build(from: original, current: current)
}
