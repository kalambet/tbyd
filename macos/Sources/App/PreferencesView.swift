import SwiftUI
import TBYDKit

// PreferencesViewModel lives in TBYDKit/PreferencesViewModel.swift

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
