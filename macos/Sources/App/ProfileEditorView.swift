import SwiftUI
import TBYDKit

struct ProfileEditorView: View {
    let appState: AppState
    @State private var viewModel = ProfileEditorViewModel()

    var body: some View {
        Form {
            Section("Communication") {
                TextField("Tone", text: $viewModel.tone)
                TextField("Detail Level", text: $viewModel.detailLevel)
                TextField("Response Length", text: $viewModel.responseLength)
            }

            Section("Technical") {
                TextField("Role", text: $viewModel.role)
                TextField("Preferred Languages", text: $viewModel.preferredLanguages)
            }

            Section("Raw JSON") {
                TextEditor(text: $viewModel.rawJSON)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 120)
            }

            HStack {
                if let error = viewModel.errorMessage {
                    Text(error)
                        .foregroundStyle(.red)
                        .font(.caption)
                }
                Spacer()
                if viewModel.saved {
                    Text("Saved")
                        .foregroundStyle(.green)
                        .font(.caption)
                }
                Button("Save") {
                    Task { await viewModel.save(client: appState.apiClient) }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!viewModel.isDirty)
                .accessibilityValue(viewModel.saved ? "Profile saved" : "")
            }
        }
        .formStyle(.grouped)
        .padding()
        .navigationTitle("Profile Editor")
        .task {
            await viewModel.load(client: appState.apiClient)
        }
    }
}

@MainActor @Observable
final class ProfileEditorViewModel {
    var tone: String = "" { didSet { checkDirty() } }
    var detailLevel: String = "" { didSet { checkDirty() } }
    var responseLength: String = "" { didSet { checkDirty() } }
    var role: String = "" { didSet { checkDirty() } }
    var preferredLanguages: String = "" { didSet { checkDirty() } }
    var rawJSON: String = "{}" { didSet { checkDirty() } }
    var errorMessage: String?
    var saved: Bool = false
    var isDirty: Bool = false

    private var originalTone: String = ""
    private var originalDetailLevel: String = ""
    private var originalResponseLength: String = ""
    private var originalRole: String = ""
    private var originalPreferredLanguages: String = ""
    private var originalJSON: String = "{}"
    private var isLoading: Bool = false

    func load(client: APIClient) async {
        isLoading = true
        defer { isLoading = false }
        do {
            let profile = try await client.getProfile()
            updateFields(from: profile)
            snapshotOriginals()
            let data = try JSONSerialization.data(withJSONObject: profile.mapValues(\.value), options: [.prettyPrinted, .sortedKeys])
            rawJSON = String(data: data, encoding: .utf8) ?? "{}"
            originalJSON = rawJSON
            isDirty = false
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func save(client: APIClient) async {
        do {
            var updates: [String: Any] = [:]
            if tone != originalTone { updates["tone"] = tone }
            if detailLevel != originalDetailLevel { updates["detail_level"] = detailLevel }
            if responseLength != originalResponseLength { updates["response_length"] = responseLength }
            if role != originalRole { updates["role"] = role }
            if preferredLanguages != originalPreferredLanguages { updates["preferred_languages"] = preferredLanguages }

            // If rawJSON changed, parse and merge all fields from it
            if rawJSON != originalJSON {
                if let data = rawJSON.data(using: .utf8),
                   let parsed = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                    for (key, value) in parsed {
                        updates[key] = value
                    }
                }
            }

            try await client.patchProfile(updates)
            saved = true
            isDirty = false
            errorMessage = nil

            await load(client: client)

            Task { [weak self] in
                try? await Task.sleep(for: .seconds(2))
                self?.saved = false
            }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func updateFields(from profile: [String: AnyCodable]) {
        tone = stringValue(profile, "tone")
        detailLevel = stringValue(profile, "detail_level")
        responseLength = stringValue(profile, "response_length")
        role = stringValue(profile, "role")
        preferredLanguages = stringValue(profile, "preferred_languages")
    }

    private func snapshotOriginals() {
        originalTone = tone
        originalDetailLevel = detailLevel
        originalResponseLength = responseLength
        originalRole = role
        originalPreferredLanguages = preferredLanguages
    }

    private func checkDirty() {
        guard !isLoading else { return }
        isDirty = tone != originalTone
            || detailLevel != originalDetailLevel
            || responseLength != originalResponseLength
            || role != originalRole
            || preferredLanguages != originalPreferredLanguages
            || rawJSON != originalJSON
    }

    private func stringValue(_ dict: [String: AnyCodable], _ key: String) -> String {
        guard let v = dict[key]?.value else { return "" }
        return "\(v)"
    }
}
