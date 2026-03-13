import Foundation

/// HTTP client for the tbyd local API server.
public actor APIClient {
    public let baseURL: URL
    private let session: URLSession
    private var token: String?

    public init(
        baseURL: URL = URL(string: "http://127.0.0.1:4000")!,
        session: URLSession = .shared,
        token: String? = nil
    ) {
        self.baseURL = baseURL
        self.session = session
        self.token = token
    }

    public func setToken(_ token: String?) {
        self.token = token
    }

    // MARK: - Health

    public struct HealthResponse: Codable, Sendable {
        public let status: String
        public let droppedInteractions: Int?

        enum CodingKeys: String, CodingKey {
            case status
            case droppedInteractions = "dropped_interactions"
        }
    }

    public func health() async throws -> HealthResponse {
        let (data, _) = try await get("/health", authenticated: false)
        return try JSONDecoder().decode(HealthResponse.self, from: data)
    }

    // MARK: - Profile

    public func getProfile() async throws -> Profile {
        let (data, _) = try await get("/profile")
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(Profile.self, from: data)
    }

    public func patchProfile(_ patch: ProfilePatch) async throws {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let body = try encoder.encode(patch)
        let _ = try await request("PATCH", path: "/profile", body: body)
    }

    /// Sends a free-form PATCH with an arbitrary dictionary (used by PreferencesViewModel
    /// for non-profile-typed fields such as `save_interactions`).
    public func patchProfileRaw(_ fields: [String: Any]) async throws {
        let body = try JSONSerialization.data(withJSONObject: fields)
        let _ = try await request("PATCH", path: "/profile", body: body)
    }

    public func deleteProfileField(path fieldPath: String) async throws {
        let _ = try await request("DELETE", path: "/profile/\(fieldPath)")
    }

    // MARK: - Interactions

    public struct Interaction: Decodable, Equatable, Sendable, Identifiable {
        public let id: String
        /// The user's original query (`user_query` in the Go model).
        public let query: String?
        /// The cloud model's response (`cloud_response` in the Go model).
        public let response: String?
        public let createdAt: String?
        public var feedbackScore: Int?
        public var feedbackNotes: String?

        enum CodingKeys: String, CodingKey {
            case id
            case query = "user_query"
            case response = "cloud_response"
            case createdAt = "created_at"
            case feedbackScore = "feedback_score"
            case feedbackNotes = "feedback_notes"
        }

        public init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            id = try container.decode(String.self, forKey: .id)
            query = try container.decodeIfPresent(String.self, forKey: .query)
            response = try container.decodeIfPresent(String.self, forKey: .response)
            createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
            feedbackNotes = try container.decodeIfPresent(String.self, forKey: .feedbackNotes)
            // Go server serialises unrated rows as feedback_score: 0; treat 0 as unrated (nil).
            let raw = try container.decodeIfPresent(Int.self, forKey: .feedbackScore)
            feedbackScore = (raw == 0) ? nil : raw
        }

        /// Memberwise initializer for constructing Interaction values directly (e.g., in tests).
        public init(
            id: String,
            query: String? = nil,
            response: String? = nil,
            createdAt: String? = nil,
            feedbackScore: Int? = nil,
            feedbackNotes: String? = nil
        ) {
            self.id = id
            self.query = query
            self.response = response
            self.createdAt = createdAt
            self.feedbackScore = feedbackScore
            self.feedbackNotes = feedbackNotes
        }

        /// Returns a copy with updated feedback fields.
        public func withFeedback(score: Int?, notes: String?) -> Interaction {
            var copy = self
            copy.feedbackScore = score
            copy.feedbackNotes = notes
            return copy
        }

        /// `true` when this interaction has a positive (thumbs-up) rating.
        public var isPositiveRated: Bool { feedbackScore == 1 }

        /// `true` when this interaction has a negative (thumbs-down) rating.
        public var isNegativeRated: Bool { feedbackScore == -1 }
    }

    public func listInteractions(limit: Int = 20, offset: Int = 0) async throws -> [Interaction] {
        let (data, _) = try await get("/interactions?limit=\(limit)&offset=\(offset)")
        return try JSONDecoder().decode([Interaction].self, from: data)
    }

    public func getInteraction(id: String) async throws -> Interaction {
        let (data, _) = try await get("/interactions/\(id)")
        return try JSONDecoder().decode(Interaction.self, from: data)
    }

    public func deleteInteraction(id: String) async throws {
        let _ = try await request("DELETE", path: "/interactions/\(id)")
    }

    public func postFeedback(interactionId: String, score: Int, notes: String?) async throws {
        struct FeedbackBody: Encodable {
            let score: Int
            let notes: String?
        }
        let body = try JSONEncoder().encode(FeedbackBody(score: score, notes: notes))
        let _ = try await request("POST", path: "/interactions/\(interactionId)/feedback", body: body)
    }

    // MARK: - Context Docs

    public struct ContextDoc: Codable, Sendable, Identifiable {
        public let id: String
        public let title: String?
        public let source: String?
        public let type: String?
        public let tags: [String]?
        public let createdAt: String?

        enum CodingKeys: String, CodingKey {
            case id, title, source, type, tags
            case createdAt = "created_at"
        }
    }

    public func listContextDocs(limit: Int = 100, offset: Int = 0) async throws -> [ContextDoc] {
        let (data, _) = try await get("/context-docs?limit=\(limit)&offset=\(offset)")
        return try JSONDecoder().decode([ContextDoc].self, from: data)
    }

    public func deleteContextDoc(id: String) async throws {
        let _ = try await request("DELETE", path: "/context-docs/\(id)")
    }

    // MARK: - Models

    public struct ModelsResponse: Codable, Sendable {
        public let data: [Model]
    }

    public struct Model: Codable, Sendable, Identifiable {
        public let id: String
    }

    public func listModels() async throws -> [Model] {
        let (data, _) = try await get("/v1/models", authenticated: false)
        let response = try JSONDecoder().decode(ModelsResponse.self, from: data)
        return response.data
    }

    // MARK: - Internal

    private func get(_ path: String, authenticated: Bool = true) async throws -> (Data, URLResponse) {
        return try await request("GET", path: path, authenticated: authenticated)
    }

    @discardableResult
    private func request(
        _ method: String,
        path: String,
        body: Data? = nil,
        authenticated: Bool = true
    ) async throws -> (Data, URLResponse) {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw APIError.invalidURL(path)
        }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.timeoutInterval = 10

        if authenticated, let token {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let (data, response) = try await session.data(for: req)

        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            throw APIError.httpError(statusCode: http.statusCode, body: String(data: data, encoding: .utf8))
        }

        return (data, response)
    }
}

public enum APIError: Error, LocalizedError {
    case httpError(statusCode: Int, body: String?)
    case invalidURL(String)

    public var errorDescription: String? {
        switch self {
        case .httpError(let code, let body):
            return "HTTP \(code): \(body ?? "no body")"
        case .invalidURL(let path):
            return "Invalid URL path: \(path)"
        }
    }
}

/// A type-erased Codable wrapper for heterogeneous JSON values.
public struct AnyCodable: Codable, @unchecked Sendable {
    public let value: Any

    public init(_ value: Any) {
        self.value = value
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            value = NSNull()
        } else if let bool = try? container.decode(Bool.self) {
            value = bool
        } else if let int = try? container.decode(Int.self) {
            value = int
        } else if let double = try? container.decode(Double.self) {
            value = double
        } else if let string = try? container.decode(String.self) {
            value = string
        } else if let array = try? container.decode([AnyCodable].self) {
            value = array.map(\.value)
        } else if let dict = try? container.decode([String: AnyCodable].self) {
            value = dict.mapValues(\.value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported type")
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case is NSNull:
            try container.encodeNil()
        case let bool as Bool:
            try container.encode(bool)
        case let int as Int:
            try container.encode(int)
        case let double as Double:
            try container.encode(double)
        case let string as String:
            try container.encode(string)
        case let array as [Any]:
            try container.encode(array.map { AnyCodable($0) })
        case let dict as [String: Any]:
            try container.encode(dict.mapValues { AnyCodable($0) })
        default:
            throw EncodingError.invalidValue(value, .init(codingPath: encoder.codingPath, debugDescription: "Unsupported type"))
        }
    }
}
