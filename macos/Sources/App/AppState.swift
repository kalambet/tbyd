import AppKit
import SwiftUI
import TBYDKit

/// Shared application state that owns the API client, process manager, and status poller.
@MainActor @Observable
final class AppState {
    let apiClient: APIClient
    let processManager: ProcessManager
    let poller: StatusPoller
    var errorMessage: String?
    var pendingDeltaCount: Int = 0
    private var terminationObserver: Any?
    private var deltaPollingTask: Task<Void, Never>?

    init() {
        let token = try? KeychainService.get(.apiToken)
        let client = APIClient(token: token)
        self.apiClient = client
        self.processManager = ProcessManager()
        self.poller = StatusPoller(client: client)
        poller.startPolling()
        startDeltaPolling()

        let pm = processManager
        terminationObserver = NotificationCenter.default.addObserver(
            forName: NSApplication.willTerminateNotification,
            object: nil,
            queue: .main
        ) { _ in
            MainActor.assumeIsolated {
                pm.stop()
            }
        }
    }

    var serverStatus: StatusPoller.Status {
        poller.status
    }

    var statusIconName: String {
        switch poller.status {
        case .unknown: "circle.fill"
        case .running: "circle.fill"
        case .stopped: "circle.fill"
        case .error: "exclamationmark.circle.fill"
        }
    }

    var statusIconColor: Color {
        if case .starting = processManager.state { return .orange }
        return switch poller.status {
        case .unknown: .gray
        case .running: .green
        case .stopped: .gray
        case .error: .red
        }
    }

    var statusLabel: String {
        if case .starting = processManager.state { return "tbyd — starting..." }
        return switch poller.status {
        case .unknown: "tbyd — checking..."
        case .running: "tbyd — running"
        case .stopped: "tbyd — stopped"
        case .error(let msg): "tbyd — error: \(msg)"
        }
    }

    var isRunning: Bool {
        poller.status == .running
    }

    private func startDeltaPolling() {
        deltaPollingTask?.cancel()
        deltaPollingTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                if self.isRunning {
                    if let deltas = try? await self.apiClient.listPendingDeltas() {
                        self.pendingDeltaCount = deltas.filter { $0.accepted == nil }.count
                    }
                }
                try? await Task.sleep(for: .seconds(60))
            }
        }
    }

    func refreshPendingDeltaCount() async {
        if let deltas = try? await apiClient.listPendingDeltas() {
            pendingDeltaCount = deltas.filter { $0.accepted == nil }.count
        }
    }

    func startServer() {
        do {
            try processManager.start()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func stopServer() {
        processManager.stop()
        errorMessage = nil
    }
}
