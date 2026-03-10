import Foundation

/// Manages the lifecycle of the tbyd Go binary.
@MainActor @Observable
public final class ProcessManager {
    public enum State: Sendable, Equatable {
        case stopped
        case starting
        case running
        case error(String)
    }

    public private(set) var state: State = .stopped
    private var process: Process?

    /// The path to the tbyd binary. Defaults to the bundled binary inside the app.
    public let binaryPath: String
    private let arguments: [String]

    public init(binaryPath: String? = nil, arguments: [String] = ["start"]) {
        self.binaryPath = binaryPath ?? Self.bundledBinaryPath()
        self.arguments = arguments
    }

    /// Resolves the path to the tbyd binary bundled inside the .app.
    private static func bundledBinaryPath() -> String {
        if let bundlePath = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("tbyd").path {
            if FileManager.default.fileExists(atPath: bundlePath) {
                return bundlePath
            }
        }
        return "/usr/local/bin/tbyd"
    }

    public func start() throws {
        guard process == nil else { return }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binaryPath)
        proc.arguments = arguments
        proc.standardOutput = FileHandle.nullDevice
        let stderrPipe = Pipe()
        proc.standardError = stderrPipe
        proc.terminationHandler = { [weak self] p in
            let exitCode = p.terminationStatus
            let reason = p.terminationReason
            let stderrData = stderrPipe.fileHandleForReading.readDataToEndOfFile()
            let stderrOutput = String(data: stderrData, encoding: .utf8)
            Task { @MainActor [weak self] in
                guard let self, self.process === p else { return }
                self.handleTermination(exitCode: exitCode, reason: reason, stderr: stderrOutput)
            }
        }

        state = .starting
        try proc.run()
        process = proc
        state = .running
    }

    public func stop() {
        guard let proc = process, proc.isRunning else {
            process = nil
            state = .stopped
            return
        }

        proc.terminate()
        process = nil
        state = .stopped
    }

    public var isRunning: Bool {
        process?.isRunning ?? false
    }

    private func handleTermination(exitCode: Int32, reason: Process.TerminationReason, stderr: String?) {
        process = nil
        if exitCode != 0 && reason != .uncaughtSignal {
            let detail = stderr.flatMap { $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            state = .error(detail ?? "Process exited with code \(exitCode)")
        } else {
            state = .stopped
        }
    }
}
