import Foundation

// MARK: - Injectable protocols

public protocol KeychainServiceProtocol: Sendable {
    func get(_ account: KeychainService.Account) throws -> String?
    func set(_ account: KeychainService.Account, value: String) throws
}

public struct DefaultKeychainService: KeychainServiceProtocol {
    public init() {}
    public func get(_ account: KeychainService.Account) throws -> String? {
        try KeychainService.get(account)
    }
    public func set(_ account: KeychainService.Account, value: String) throws {
        try KeychainService.set(account, value: value)
    }
}

public protocol LaunchAgentManagerProtocol: Sendable {
    var isEnabled: Bool { get }
    func setEnabled(_ enabled: Bool) throws
}

public struct DefaultLaunchAgentManager: LaunchAgentManagerProtocol {
    public init() {}
    public var isEnabled: Bool { LaunchAgentManager.isEnabled }
    public func setEnabled(_ enabled: Bool) throws {
        try LaunchAgentManager.setEnabled(enabled)
    }
}

public protocol ConfigServiceProtocol: Sendable {
    func readValues() async -> [String: String]
    func setValue(_ key: String, value: String) async throws
}

public final class DefaultConfigService: ConfigServiceProtocol, @unchecked Sendable {
    private let binaryPath: String

    public init(binaryPath: String) {
        self.binaryPath = binaryPath
    }

    public func readValues() async -> [String: String] {
        let path = binaryPath
        return await (try? Task.detached {
            let process = Process()
            let pipe = Pipe()
            process.executableURL = URL(fileURLWithPath: path)
            process.arguments = ["config", "show"]
            process.standardOutput = pipe
            process.standardError = FileHandle.nullDevice
            try process.run()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            process.waitUntilExit()
            guard let output = String(data: data, encoding: .utf8) else { return [:] }

            var result: [String: String] = [:]
            for line in output.components(separatedBy: .newlines) {
                let parts = line.split(separator: "=", maxSplits: 1)
                if parts.count == 2 {
                    result[parts[0].trimmingCharacters(in: .whitespaces)] = parts[1].trimmingCharacters(in: .whitespaces)
                }
            }
            return result
        }.value) ?? [:]
    }

    public func setValue(_ key: String, value: String) async throws {
        let path = binaryPath
        try await Task.detached {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: path)
            process.arguments = ["config", "set", key, value]
            process.standardOutput = FileHandle.nullDevice
            process.standardError = FileHandle.nullDevice
            try process.run()
            process.waitUntilExit()
            guard process.terminationStatus == 0 else {
                throw DefaultConfigService.ConfigError.commandFailed(key: key, exitCode: process.terminationStatus)
            }
        }.value
    }

    public enum ConfigError: LocalizedError {
        case commandFailed(key: String, exitCode: Int32)

        public var errorDescription: String? {
            switch self {
            case let .commandFailed(key, exitCode):
                return "tbyd config set \(key) failed (exit \(exitCode))"
            }
        }
    }
}

// MARK: - ViewModel

@MainActor @Observable
public final class PreferencesViewModel {
    public var apiKey: String = ""
    public var selectedModel: String = ""
    public var fastModel: String = ""
    public var deepModel: String = ""
    public var saveInteractions: Bool = false
    public var autoStart: Bool = false
    public var availableModels: [APIClient.Model] = []
    public var errorMessage: String?

    private let keychain: any KeychainServiceProtocol
    private let launchAgent: any LaunchAgentManagerProtocol
    private let configService: any ConfigServiceProtocol
    private var apiClient: APIClient?

    public init(
        keychain: any KeychainServiceProtocol = DefaultKeychainService(),
        launchAgent: any LaunchAgentManagerProtocol = DefaultLaunchAgentManager(),
        configService: (any ConfigServiceProtocol)? = nil
    ) {
        self.keychain = keychain
        self.launchAgent = launchAgent
        self.configService = configService ?? DefaultConfigService(binaryPath: ProcessManager().binaryPath)
    }

    public func load(client: APIClient) async {
        self.apiClient = client
        apiKey = (try? keychain.get(.openRouterAPIKey)) ?? ""
        autoStart = launchAgent.isEnabled

        await loadConfigValues()

        do {
            availableModels = try await client.listModels()
        } catch {
            // Server may not be running.
        }
    }

    public func saveAPIKey() {
        do {
            try keychain.set(.openRouterAPIKey, value: apiKey)
            errorMessage = nil
        } catch {
            errorMessage = "Failed to save API key: \(error.localizedDescription)"
        }
    }

    public func setAutoStart(_ enabled: Bool) {
        do {
            try launchAgent.setEnabled(enabled)
            errorMessage = nil
        } catch {
            errorMessage = "Failed to update auto-start: \(error.localizedDescription)"
        }
    }

    public func setSaveInteractions(_ enabled: Bool) async {
        guard let client = apiClient else {
            await setConfigValue("storage.save_interactions", value: enabled ? "true" : "false")
            return
        }
        do {
            try await client.patchProfile(["save_interactions": enabled])
            errorMessage = nil
        } catch {
            errorMessage = "Failed to update save interactions: \(error.localizedDescription)"
            return
        }
        // Keep local config in sync so loadConfigValues reads the correct value on next launch.
        await setConfigValue("storage.save_interactions", value: enabled ? "true" : "false")
    }

    public func saveFastModel() async {
        await setConfigValue("ollama.fast_model", value: fastModel)
    }

    public func saveDeepModel() async {
        await setConfigValue("ollama.deep_model", value: deepModel)
    }

    public func saveSelectedModel() async {
        await setConfigValue("proxy.default_model", value: selectedModel)
    }

    // MARK: - tbyd config helpers

    private func loadConfigValues() async {
        let values = await configService.readValues()
        if let v = values["storage.save_interactions"] {
            saveInteractions = v.lowercased() == "true"
        }
        if let v = values["ollama.fast_model"], !v.isEmpty {
            fastModel = v
        }
        if let v = values["ollama.deep_model"], !v.isEmpty {
            deepModel = v
        }
        if let v = values["proxy.default_model"], !v.isEmpty {
            selectedModel = v
        }
    }

    private func setConfigValue(_ key: String, value: String) async {
        do {
            try await configService.setValue(key, value: value)
            errorMessage = nil
        } catch {
            errorMessage = "Failed to update \(key): \(error.localizedDescription)"
        }
    }
}
