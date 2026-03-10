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
                [{"id":"i1","query":"hello","summary":"greeting","created_at":"2025-01-01"},
                 {"id":"i2","query":"world","summary":"planet","created_at":"2025-01-02"}]
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
                [{"id":"i1","query":"hello","summary":"greeting"},
                 {"id":"i2","query":"world","summary":"planet"}]
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
                [{"id":"i1","query":"go preferences","summary":"Go lang prefs"},
                 {"id":"i2","query":"python setup","summary":"Python config"}]
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
