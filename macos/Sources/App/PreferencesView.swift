import SwiftUI
import TBYDKit

struct PreferencesView: View {
    let appState: AppState
    @State private var viewModel = PreferencesViewModel()

    var body: some View {
        Form {
            Section("OpenRouter") {
                SecureField("API Key", text: $viewModel.apiKey)
                    .onSubmit { viewModel.saveAPIKey() }

                if !viewModel.availableModels.isEmpty {
                    Picker("Default Cloud Model", selection: $viewModel.selectedModel) {
                        ForEach(viewModel.availableModels) { model in
                            Text(model.id).tag(model.id)
                        }
                    }
                    .onChange(of: viewModel.selectedModel) { _, _ in
                        Task { await viewModel.saveSelectedModel() }
                    }
                } else {
                    Text("Connect to load models")
                        .foregroundStyle(.secondary)
                }
            }

            Section("Local Models") {
                TextField("Fast Model", text: $viewModel.fastModel)
                    .onSubmit { Task { await viewModel.saveFastModel() } }
                TextField("Deep Model", text: $viewModel.deepModel)
                    .onSubmit { Task { await viewModel.saveDeepModel() } }
            }

            Section("Storage") {
                Toggle("Save interactions", isOn: $viewModel.saveInteractions)
                    .onChange(of: viewModel.saveInteractions) { _, newValue in
                        Task { await viewModel.setSaveInteractions(newValue) }
                    }
            }

            Section("Startup") {
                Toggle("Auto-start at login", isOn: $viewModel.autoStart)
                    .onChange(of: viewModel.autoStart) { _, newValue in
                        viewModel.setAutoStart(newValue)
                    }
            }

            if let error = viewModel.errorMessage {
                Text(error)
                    .foregroundStyle(.red)
                    .font(.caption)
            }
        }
        .formStyle(.grouped)
        .padding()
        .task {
            await viewModel.load(client: appState.apiClient)
        }
    }
}

@MainActor @Observable
final class PreferencesViewModel {
    var apiKey: String = ""
    var selectedModel: String = ""
    var fastModel: String = ""
    var deepModel: String = ""
    var saveInteractions: Bool = false
    var autoStart: Bool = false
    var availableModels: [APIClient.Model] = []
    var errorMessage: String?

    private var tbydBinaryPath: String {
        ProcessManager().binaryPath
    }

    func load(client: APIClient) async {
        apiKey = (try? KeychainService.get(.openRouterAPIKey)) ?? ""
        autoStart = LaunchAgentManager.isEnabled

        await loadConfigValues()

        do {
            availableModels = try await client.listModels()
        } catch {
            // Server may not be running.
        }
    }

    func saveAPIKey() {
        do {
            try KeychainService.set(.openRouterAPIKey, value: apiKey)
            errorMessage = nil
        } catch {
            errorMessage = "Failed to save API key: \(error.localizedDescription)"
        }
    }

    func setAutoStart(_ enabled: Bool) {
        do {
            try LaunchAgentManager.setEnabled(enabled)
            errorMessage = nil
        } catch {
            errorMessage = "Failed to update auto-start: \(error.localizedDescription)"
        }
    }

    func setSaveInteractions(_ enabled: Bool) async {
        await setConfigValue("storage.save_interactions", value: enabled ? "true" : "false")
    }

    func saveFastModel() async {
        await setConfigValue("ollama.fast_model", value: fastModel)
    }

    func saveDeepModel() async {
        await setConfigValue("ollama.deep_model", value: deepModel)
    }

    func saveSelectedModel() async {
        await setConfigValue("proxy.default_model", value: selectedModel)
    }

    // MARK: - tbyd config helpers

    private func loadConfigValues() async {
        let values = await runTbydConfigShow()
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
        let path = tbydBinaryPath
        do {
            try await Task.detached {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: path)
                process.arguments = ["config", "set", key, value]
                process.standardOutput = FileHandle.nullDevice
                process.standardError = FileHandle.nullDevice
                try process.run()
                process.waitUntilExit()
                guard process.terminationStatus == 0 else {
                    throw ConfigError.commandFailed(key: key, exitCode: process.terminationStatus)
                }
            }.value
            errorMessage = nil
        } catch {
            errorMessage = "Failed to update \(key): \(error.localizedDescription)"
        }
    }

    private func runTbydConfigShow() async -> [String: String] {
        let path = tbydBinaryPath
        return await (try? Task.detached {
            let process = Process()
            let pipe = Pipe()
            process.executableURL = URL(fileURLWithPath: path)
            process.arguments = ["config", "show"]
            process.standardOutput = pipe
            process.standardError = FileHandle.nullDevice
            try process.run()
            process.waitUntilExit()

            let data = pipe.fileHandleForReading.readDataToEndOfFile()
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

    private enum ConfigError: LocalizedError {
        case commandFailed(key: String, exitCode: Int32)

        var errorDescription: String? {
            switch self {
            case let .commandFailed(key, exitCode):
                return "tbyd config set \(key) failed (exit \(exitCode))"
            }
        }
    }
}
