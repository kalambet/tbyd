import Foundation

/// Lightweight HTTP client used exclusively by the Share Extension to POST to /ingest.
/// Separate from `APIClient` so the extension target has no dependency on TBYDKit.
/// App Group suite name shared between the menubar app and the Share Extension.
/// Allows the menubar app to write the configured server port so the extension
/// can pick it up without recompiling. Requires the App Group entitlement on both targets.
let sharedDefaultsSuite = "group.com.kalambet.tbyd"

public actor IngestClient {
    private let ingestURL: URL
    private let session: URLSession

    /// Builds the ingest URL, reading the server port from shared App Group `UserDefaults`
    /// (key `"tbydServerPort"`) so the extension respects user-configured ports.
    /// Falls back to 4000 if the key is absent or the suite is unavailable.
    public init(session: URLSession = .shared) {
        let stored = UserDefaults(suiteName: sharedDefaultsSuite)?.integer(forKey: "tbydServerPort") ?? 0
        let port = stored > 0 ? stored : 4000
        guard let url = URL(string: "http://localhost:\(port)/ingest") else {
            fatalError("Could not construct ingest URL for port \(port)")
        }
        self.ingestURL = url
        self.session = session
    }

    /// Designated initialiser for tests — accepts an explicit URL string and session.
    public init(ingestURLString: String, session: URLSession) {
        guard let url = URL(string: ingestURLString) else {
            fatalError("Invalid URL string for IngestClient: \(ingestURLString)")
        }
        self.ingestURL = url
        self.session = session
    }

    /// Posts an `IngestRequest` to `/ingest`.
    ///
    /// - Throws: `URLError` when the server is not reachable, or `IngestError.httpError` on non-2xx.
    public func post(_ request: IngestRequest) async throws {
        let encoder = JSONEncoder()
        let body = try encoder.encode(request)

        var urlRequest = URLRequest(url: ingestURL)
        urlRequest.httpMethod = "POST"
        urlRequest.httpBody = body
        urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        urlRequest.timeoutInterval = 10

        let (data, response) = try await session.data(for: urlRequest)

        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let body = String(data: data, encoding: .utf8)
            throw IngestError.httpError(statusCode: http.statusCode, body: body)
        }
    }
}

public enum IngestError: Error, LocalizedError {
    case httpError(statusCode: Int, body: String?)

    public var errorDescription: String? {
        switch self {
        case .httpError(let code, let body):
            return "HTTP \(code): \(body ?? "no body")"
        }
    }
}
