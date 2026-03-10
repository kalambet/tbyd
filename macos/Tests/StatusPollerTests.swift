import Testing
import Foundation
@testable import TBYDKit

@Suite("StatusPoller", .serialized)
struct StatusPollerTests {

    @Test("Transitions to running on 200 health response")
    @MainActor
    func transitionsToRunning() async throws {
        let session = makeSession(for: StatusPollerMock.self)
        StatusPollerMock.handler = { request in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            let body = #"{"status":"ok"}"#.data(using: .utf8)!
            return (response, body)
        }

        let client = APIClient(session: session)
        let poller = StatusPoller(client: client, interval: 60)
        await poller.poll()
        #expect(poller.status == .running)
    }

    @Test("Transitions to stopped on connection error")
    @MainActor
    func transitionsToStopped() async throws {
        let session = makeSession(for: StatusPollerMock.self)
        StatusPollerMock.handler = { _ in
            throw URLError(.cannotConnectToHost)
        }

        let client = APIClient(session: session)
        let poller = StatusPoller(client: client, interval: 60)
        await poller.poll()
        #expect(poller.status == .stopped)
    }

    @Test("Transitions to error on unexpected status")
    @MainActor
    func transitionsToError() async throws {
        let session = makeSession(for: StatusPollerMock.self)
        StatusPollerMock.handler = { request in
            let url = request.url!
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!
            let body = #"{"status":"degraded"}"#.data(using: .utf8)!
            return (response, body)
        }

        let client = APIClient(session: session)
        let poller = StatusPoller(client: client, interval: 60)
        await poller.poll()
        if case .error = poller.status {
            // expected
        } else {
            Issue.record("Expected error status, got \(poller.status)")
        }
    }
}
