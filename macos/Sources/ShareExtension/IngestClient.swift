import Foundation

/// Lightweight HTTP client used exclusively by the Share Extension to POST to /ingest.
/// Separate from `APIClient` so the extension target has no dependency on TBYDKit.
public actor IngestClient {
    private let ingestURL: URL
    private let session: URLSession

    public init(
        ingestURL: URL = URL(string: "http://localhost:4000/ingest")!,
        session: URLSession = .shared
    ) {
        self.ingestURL = ingestURL
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
