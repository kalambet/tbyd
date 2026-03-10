import Foundation

/// Polls the tbyd health endpoint and reports server status.
@MainActor @Observable
public final class StatusPoller {
    public enum Status: Sendable, Equatable {
        case unknown
        case running
        case stopped
        case error(String)
    }

    public private(set) var status: Status = .unknown

    private let client: APIClient
    private let interval: TimeInterval
    private var task: Task<Void, Never>?

    public init(client: APIClient, interval: TimeInterval = 5.0) {
        self.client = client
        self.interval = interval
    }

    public func startPolling() {
        guard task == nil else { return }
        let interval = self.interval
        task = Task { [weak self] in
            while !Task.isCancelled {
                await self?.poll()
                try? await Task.sleep(for: .seconds(interval))
            }
        }
    }

    public func stopPolling() {
        task?.cancel()
        task = nil
    }

    public func poll() async {
        do {
            let response = try await client.health()
            if response.status == "ok" {
                status = .running
            } else {
                status = .error("Unexpected status: \(response.status)")
            }
        } catch is CancellationError {
            return
        } catch {
            status = .stopped
        }
    }
}
