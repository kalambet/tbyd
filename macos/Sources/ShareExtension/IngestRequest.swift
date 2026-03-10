import Foundation

/// The POST body sent to `http://localhost:4000/ingest` from the Share Extension.
public struct IngestRequest: Encodable, Sendable {
    public let source: String   // always "share_extension"
    public let type: String     // "text", "url", "file"
    public let title: String?
    public let content: String?
    public let url: String?
    public let tags: [String]
    public let metadata: [String: String]

    public init(
        source: String = "share_extension",
        type: String,
        title: String? = nil,
        content: String? = nil,
        url: String? = nil,
        tags: [String] = [],
        metadata: [String: String] = [:]
    ) {
        self.source = source
        self.type = type
        self.title = title
        self.content = content
        self.url = url
        self.tags = tags
        self.metadata = metadata
    }
}
