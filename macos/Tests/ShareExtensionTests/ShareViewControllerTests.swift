import Testing
import Foundation
import UniformTypeIdentifiers
@testable import ShareExtension

// MARK: - Test helpers

/// Creates an IngestClient backed by the mock URLSession.
private func makeMockIngestClient() -> IngestClient {
    IngestClient(session: makeSession(for: ShareExtensionMock.self))
}

/// Decodes the captured POST body into a JSON dictionary.
private func decodeBody(_ data: Data) throws -> [String: Any] {
    let obj = try JSONSerialization.jsonObject(with: data)
    return obj as? [String: Any] ?? [:]
}

// MARK: - Suite

@Suite("ShareViewController", .serialized)
@MainActor
struct ShareViewControllerTests {

    // MARK: - testTextItem
    //
    // Inject a plain-text SharedItem (simulating what resolveItem produces from
    // an NSExtensionItem carrying public.text), mock URLSession, verify POST body.

    @Test("Sends type=text and correct content for plain-text item")
    func testTextItem() async throws {
        var capturedBody: Data?

        ShareExtensionMock.handler = { _, body in
            capturedBody = body
            let url = URL(string: "http://localhost:4000/ingest")!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        let svc = ShareViewController()
        svc.ingestClient = makeMockIngestClient()
        svc.loadView()

        let text = "Hello, tbyd!"
        svc.resolvedItem = SharedItem(
            type: "text",
            preview: text,
            title: "Test title",
            content: text
        )

        await svc.handleSave()

        let body = try #require(capturedBody)
        let json = try decodeBody(body)
        #expect(json["type"] as? String == "text")
        #expect(json["content"] as? String == text)
        #expect(json["source"] as? String == "share_extension")
    }

    // MARK: - testURLItem

    @Test("Sends type=url and URL string for web URL item")
    func testURLItem() async throws {
        var capturedBody: Data?

        ShareExtensionMock.handler = { _, body in
            capturedBody = body
            let url = URL(string: "http://localhost:4000/ingest")!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        let svc = ShareViewController()
        svc.ingestClient = makeMockIngestClient()
        svc.loadView()

        let webURL = "https://example.com/article"
        svc.resolvedItem = SharedItem(
            type: "url",
            preview: webURL,
            title: "Example Article",
            urlString: webURL
        )

        await svc.handleSave()

        let body = try #require(capturedBody)
        let json = try decodeBody(body)
        #expect(json["type"] as? String == "url")
        #expect(json["url"] as? String == webURL)
    }

    // MARK: - testFileItem

    @Test("Sends type=file and base64-encoded content for a PDF file")
    func testFileItem() async throws {
        var capturedBody: Data?

        ShareExtensionMock.handler = { _, body in
            capturedBody = body
            let url = URL(string: "http://localhost:4000/ingest")!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        let svc = ShareViewController()
        svc.ingestClient = makeMockIngestClient()
        svc.loadView()

        let fileContent = Data("PDF content here".utf8)
        let expectedBase64 = fileContent.base64EncodedString()

        svc.resolvedItem = SharedItem(
            type: "file",
            preview: "test.pdf (0 KB)",
            title: "test.pdf",
            content: expectedBase64,
            metadata: [
                "filename": "test.pdf",
                "extension": "pdf",
                "mime_type": "application/pdf",
                "byte_count": "16",
            ]
        )

        await svc.handleSave()

        let body = try #require(capturedBody)
        let json = try decodeBody(body)
        #expect(json["type"] as? String == "file")
        #expect(json["content"] as? String == expectedBase64)
        let metadata = json["metadata"] as? [String: String]
        #expect(metadata?["mime_type"] == "application/pdf")
        #expect(metadata?["byte_count"] == "16")
    }

    // MARK: - testServerNotRunning

    @Test("Shows error message when server is not running")
    func testServerNotRunning() async throws {
        ShareExtensionMock.handler = { _, _ in
            throw URLError(.cannotConnectToHost)
        }

        let svc = ShareViewController()
        svc.ingestClient = makeMockIngestClient()
        svc.loadView()

        svc.resolvedItem = SharedItem(
            type: "text",
            preview: "hello",
            content: "hello"
        )

        await svc.handleSave()

        #expect(svc.errorLabel.stringValue == "tbyd is not running. Start it from the menubar.")
        #expect(svc.errorLabel.isHidden == false)
    }

    // MARK: - testLargeFileWarning

    @Test("Shows warning and does NOT post when file exceeds 10 MB")
    func testLargeFileWarning() async throws {
        var postWasCalled = false

        ShareExtensionMock.handler = { _, _ in
            postWasCalled = true
            let url = URL(string: "http://localhost:4000/ingest")!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        let svc = ShareViewController()
        svc.ingestClient = makeMockIngestClient()
        svc.loadView()

        svc.resolvedItem = SharedItem(
            type: "file",
            preview: "big.bin (11 MB)",
            title: "big.bin",
            isOversized: true
        )

        await svc.handleSave()

        #expect(postWasCalled == false)
        #expect(svc.errorLabel.stringValue.contains("10 MB"))
        #expect(svc.errorLabel.isHidden == false)
    }

    // MARK: - testTagsParsed

    @Test("Parses comma-separated tags and includes them in the POST body")
    func testTagsParsed() async throws {
        var capturedBody: Data?

        ShareExtensionMock.handler = { _, body in
            capturedBody = body
            let url = URL(string: "http://localhost:4000/ingest")!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        let svc = ShareViewController()
        svc.ingestClient = makeMockIngestClient()
        svc.loadView()

        svc.tagsField.stringValue = "go,privacy"
        svc.resolvedItem = SharedItem(
            type: "text",
            preview: "test",
            content: "test"
        )

        #expect(svc.parsedTags == ["go", "privacy"])

        await svc.handleSave()

        let body = try #require(capturedBody)
        let json = try decodeBody(body)
        let receivedTags = json["tags"] as? [String]
        #expect(receivedTags == ["go", "privacy"])
    }
}
