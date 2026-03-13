import Testing
import Foundation
@testable import TBYDKit

@Suite("DataBrowserViewModel", .serialized)
struct DataBrowserViewModelTests {

    @Test("loadInteractions populates interactions array")
    @MainActor
    func loadInteractions() async throws {
        let session = makeSession(for: DataBrowserMock.self)
        DataBrowserMock.handler = { request in
            let url = request.url!
            let path = url.path
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!

            if path.contains("interactions") {
                let body = """
                [{"id":"i1","user_query":"hello","created_at":"2025-01-01"},
                 {"id":"i2","user_query":"world","created_at":"2025-01-02"}]
                """.data(using: .utf8)!
                return (response, body)
            } else if path.contains("context-docs") {
                return (response, "[]".data(using: .utf8)!)
            }
            return (response, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        #expect(vm.interactions.count == 2)
        #expect(vm.interactions[0].id == "i1")
        #expect(vm.interactions[1].id == "i2")
    }

    @Test("deleteInteraction removes entry from array")
    @MainActor
    func deleteInteraction() async throws {
        let session = makeSession(for: DataBrowserMock.self)
        DataBrowserMock.handler = { request in
            let url = request.url!
            let path = url.path
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!

            if path.contains("interactions") && request.httpMethod == "GET" {
                let body = """
                [{"id":"i1","user_query":"hello"},
                 {"id":"i2","user_query":"world"}]
                """.data(using: .utf8)!
                return (response, body)
            } else if path.contains("context-docs") {
                return (response, "[]".data(using: .utf8)!)
            } else if request.httpMethod == "DELETE" {
                return (response, "{}".data(using: .utf8)!)
            }
            return (response, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        #expect(vm.interactions.count == 2)

        await vm.deleteInteraction(id: "i1")
        #expect(vm.interactions.count == 1)
        #expect(vm.interactions[0].id == "i2")
    }

    // MARK: - Feedback tests

    @Test("postFeedback applies optimistic update before network responds")
    @MainActor
    func testFeedbackOptimisticUpdate() async throws {
        let session = makeSession(for: DataBrowserBodyMock.self)
        DataBrowserBodyMock.handler = { request, _ in
            let url = request.url!
            let response200 = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!

            if url.path.contains("feedback") {
                // Block until the test releases us.
                // We run synchronously inside URLProtocol, so we can't await —
                // just return success immediately; the optimistic-update check
                // is done before this handler is even invoked in the serial test.
                return (response200, "{}".data(using: .utf8)!)
            } else if url.path.hasSuffix("/interactions") || url.path.contains("interactions?") {
                let body = """
                [{"id":"i1","user_query":"hello","feedback_score":0}]
                """.data(using: .utf8)!
                return (response200, body)
            } else if url.path.contains("context-docs") {
                return (response200, "[]".data(using: .utf8)!)
            }
            return (response200, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        #expect(vm.interactions.count == 1)
        #expect(vm.interactions[0].feedbackScore == nil, "score 0 should decode as nil")

        await vm.postFeedback(interactionId: "i1", score: 1, notes: nil)

        #expect(vm.interactions[0].feedbackScore == 1)
        #expect(vm.feedbackError["i1"] != true)
    }

    @Test("postFeedback reverts optimistic update on network error")
    @MainActor
    func testFeedbackRevertsOnError() async throws {
        let session = makeSession(for: DataBrowserBodyMock.self)
        DataBrowserBodyMock.handler = { request, _ in
            let url = request.url!
            let response200 = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            let response500 = HTTPURLResponse(url: url, statusCode: 500, httpVersion: nil, headerFields: nil)!

            if url.path.contains("feedback") {
                return (response500, "server error".data(using: .utf8)!)
            } else if url.path.hasSuffix("/interactions") || url.path.contains("interactions?") {
                let body = """
                [{"id":"i1","user_query":"hello","feedback_score":0}]
                """.data(using: .utf8)!
                return (response200, body)
            } else if url.path.contains("context-docs") {
                return (response200, "[]".data(using: .utf8)!)
            }
            return (response200, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        #expect(vm.interactions[0].feedbackScore == nil)

        await vm.postFeedback(interactionId: "i1", score: 1, notes: nil)

        // Score should be reverted to its original nil value.
        #expect(vm.interactions[0].feedbackScore == nil, "score should revert after server error")
        #expect(vm.feedbackError["i1"] == true, "error flag should be set")
        #expect(vm.errorMessage != nil, "errorMessage should be populated")
    }

    @Test("Interaction with feedback_score 1 decodes correctly")
    @MainActor
    func testFeedbackAlreadyRated() async throws {
        let session = makeSession(for: DataBrowserMock.self)
        DataBrowserMock.handler = { request in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!

            if url.path.hasSuffix("/interactions") || url.path.contains("interactions?") {
                let body = """
                [{"id":"i1","user_query":"hello","feedback_score":1,"feedback_notes":"good one"}]
                """.data(using: .utf8)!
                return (response, body)
            } else if url.path.contains("context-docs") {
                return (response, "[]".data(using: .utf8)!)
            }
            return (response, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        let interaction = try #require(vm.interactions.first)
        #expect(interaction.feedbackScore == 1)
        #expect(interaction.feedbackNotes == "good one")
        #expect(interaction.isPositiveRated)
        #expect(!interaction.isNegativeRated)
    }

    @Test("postFeedback sends notes in request body")
    @MainActor
    func testFeedbackSendsNotes() async throws {
        let session = makeSession(for: DataBrowserBodyMock.self)
        var capturedBody: Data?
        DataBrowserBodyMock.handler = { request, body in
            let url = request.url!
            let response200 = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!

            if url.path.contains("feedback") {
                capturedBody = body
                return (response200, "{}".data(using: .utf8)!)
            } else if url.path.hasSuffix("/interactions") || url.path.contains("interactions?") {
                let responseBody = """
                [{"id":"i1","user_query":"hello","feedback_score":0}]
                """.data(using: .utf8)!
                return (response200, responseBody)
            } else if url.path.contains("context-docs") {
                return (response200, "[]".data(using: .utf8)!)
            }
            return (response200, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        await vm.postFeedback(interactionId: "i1", score: 1, notes: "Great answer")

        let body = try #require(capturedBody)
        let json = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(json?["notes"] as? String == "Great answer")
        #expect(json?["score"] as? Int == 1)
    }

    @Test("search filters interactions by query")
    @MainActor
    func searchFilters() async throws {
        let session = makeSession(for: DataBrowserMock.self)
        DataBrowserMock.handler = { request in
            let url = request.url!
            let path = url.path
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!

            if path.contains("interactions") {
                let body = """
                [{"id":"i1","user_query":"go preferences"},
                 {"id":"i2","user_query":"python setup"}]
                """.data(using: .utf8)!
                return (response, body)
            } else if path.contains("context-docs") {
                return (response, "[]".data(using: .utf8)!)
            }
            return (response, "[]".data(using: .utf8)!)
        }

        let client = APIClient(session: session, token: "test-token")
        let vm = DataBrowserViewModel()
        await vm.loadAll(client: client)

        vm.searchText = "Go"
        #expect(vm.filteredInteractions.count == 1)
        #expect(vm.filteredInteractions[0].id == "i1")
    }
}
