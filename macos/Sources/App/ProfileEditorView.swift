import SwiftUI
import TBYDKit

// MARK: - Supporting types

/// UUID-stable wrapper for any single text value in an editor list.
/// Duplicate text values are valid (e.g., two opinions that happen to read the same);
/// identity is provided by `id`, never by text content.
struct EditableItem: Identifiable {
    let id: UUID
    var text: String

    init(id: UUID = UUID(), text: String) {
        self.id = id
        self.text = text
    }
}

struct ExpertiseEntry: Identifiable {
    let id: UUID
    var skill: String
    var level: String

    init(id: UUID = UUID(), skill: String = "", level: String = "intermediate") {
        self.id = id
        self.skill = skill
        self.level = level
    }
}

// MARK: - ProfileEditorView

struct ProfileEditorView: View {
    let appState: AppState
    @State private var viewModel = ProfileEditorViewModel()

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                identitySection
                communicationSection
                interestsSection
                opinionsSection
                preferencesSection
                rawJSONSection
                footerSection
            }
            .padding()
        }
        .navigationTitle("Profile Editor")
        .task {
            await viewModel.load(client: appState.apiClient)
        }
    }

    // MARK: - Identity section

    private var identitySection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                sectionHeader("Identity")

                LabeledContent("Role") {
                    TextField("e.g. Senior Engineer", text: $viewModel.role)
                        .textFieldStyle(.roundedBorder)
                }

                Divider()

                VStack(alignment: .leading, spacing: 6) {
                    Text("Expertise")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)

                    ForEach($viewModel.expertise) { $entry in
                        HStack(spacing: 8) {
                            TextField("Skill", text: $entry.skill)
                                .textFieldStyle(.roundedBorder)
                                .frame(maxWidth: .infinity)
                                .accessibilityLabel("Skill name")

                            Picker("Level", selection: $entry.level) {
                                Text("Beginner").tag("beginner")
                                Text("Intermediate").tag("intermediate")
                                Text("Expert").tag("expert")
                            }
                            .pickerStyle(.menu)
                            .frame(width: 140)
                            .accessibilityLabel("Expertise level for \(entry.skill)")

                            Button {
                                viewModel.removeExpertise(entry)
                            } label: {
                                Image(systemName: "minus.circle.fill")
                                    .foregroundStyle(.red)
                            }
                            .buttonStyle(.borderless)
                            .accessibilityLabel("Remove \(entry.skill) expertise")
                        }
                    }

                    Button {
                        viewModel.addExpertise()
                    } label: {
                        Label("Add Skill", systemImage: "plus.circle")
                    }
                    .buttonStyle(.borderless)
                }

                Divider()

                // Issue 2 fix: currentProjects uses EditableItem, not [String] with id: \.self.
                editableItemList(
                    label: "Current Projects",
                    items: $viewModel.currentProjects,
                    placeholder: "Project name",
                    moveAction: { viewModel.moveCurrentProjects(from: $0, to: $1) },
                    deleteAction: { viewModel.deleteCurrentProjects(at: $0) },
                    addAction: { viewModel.addCurrentProject() }
                )
            }
            .padding(.vertical, 4)
        }
        .padding(.bottom, 12)
    }

    // MARK: - Communication section

    private var communicationSection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                sectionHeader("Communication")

                LabeledContent("Tone") {
                    Picker("Tone", selection: $viewModel.tone) {
                        Text("Not set").tag(Optional<String>.none)
                        Text("Direct").tag(Optional<String>.some("direct"))
                        Text("Friendly").tag(Optional<String>.some("friendly"))
                        Text("Formal").tag(Optional<String>.some("formal"))
                    }
                    .pickerStyle(.menu)
                    .frame(maxWidth: 200, alignment: .leading)
                    .accessibilityLabel("Communication tone")
                }

                LabeledContent("Detail Level") {
                    Picker("Detail Level", selection: $viewModel.detailLevel) {
                        Text("Not set").tag(Optional<String>.none)
                        Text("Concise").tag(Optional<String>.some("concise"))
                        Text("Balanced").tag(Optional<String>.some("balanced"))
                        Text("Thorough").tag(Optional<String>.some("thorough"))
                    }
                    .pickerStyle(.menu)
                    .frame(maxWidth: 200, alignment: .leading)
                    .accessibilityLabel("Detail level preference")
                }

                LabeledContent("Format") {
                    Picker("Format", selection: $viewModel.format) {
                        Text("Not set").tag(Optional<String>.none)
                        Text("Prose").tag(Optional<String>.some("prose"))
                        Text("Markdown").tag(Optional<String>.some("markdown"))
                        Text("Structured").tag(Optional<String>.some("structured"))
                    }
                    .pickerStyle(.menu)
                    .frame(maxWidth: 200, alignment: .leading)
                    .accessibilityLabel("Response format preference")
                }
            }
            .padding(.vertical, 4)
        }
        .padding(.bottom, 12)
    }

    // MARK: - Interests section

    private var interestsSection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                sectionHeader("Interests")

                // Issue 2 + 4 fix: interests use EditableItem with editable TextFields,
                // not Text(...) chips with id: \.self.
                editableItemList(
                    label: "Primary",
                    items: $viewModel.primaryInterests,
                    placeholder: "e.g. privacy, distributed systems",
                    moveAction: { viewModel.movePrimaryInterests(from: $0, to: $1) },
                    deleteAction: { viewModel.deletePrimaryInterests(at: $0) },
                    addAction: { viewModel.addPrimaryInterest() }
                )

                Divider()

                editableItemList(
                    label: "Emerging",
                    items: $viewModel.emergingInterests,
                    placeholder: "e.g. AI safety, WebAssembly",
                    moveAction: { viewModel.moveEmergingInterests(from: $0, to: $1) },
                    deleteAction: { viewModel.deleteEmergingInterests(at: $0) },
                    addAction: { viewModel.addEmergingInterest() }
                )
            }
            .padding(.vertical, 4)
        }
        .padding(.bottom, 12)
    }

    // MARK: - Opinions section

    private var opinionsSection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 8) {
                sectionHeader("Opinions")
                Text("Reorder by dragging. Each item is limited to 500 characters.")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                editableItemList(
                    items: $viewModel.opinions,
                    placeholder: "Add an opinion…",
                    moveAction: { viewModel.moveOpinions(from: $0, to: $1) },
                    deleteAction: { viewModel.deleteOpinions(at: $0) },
                    addAction: { viewModel.addOpinion() }
                )
            }
            .padding(.vertical, 4)
        }
        .padding(.bottom, 12)
    }

    // MARK: - Preferences section

    private var preferencesSection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 8) {
                sectionHeader("Preferences")
                Text("Reorder by dragging. Items higher in the list have higher priority.")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                editableItemList(
                    items: $viewModel.preferences,
                    placeholder: "Add a preference…",
                    moveAction: { viewModel.movePreferences(from: $0, to: $1) },
                    deleteAction: { viewModel.deletePreferences(at: $0) },
                    addAction: { viewModel.addPreference() }
                )
            }
            .padding(.vertical, 4)
        }
        .padding(.bottom, 12)
    }

    // MARK: - Shared editable list

    /// Issue 6 fix: uses `ForEach` without a `List` wrapper so there is no nested-scroll
    /// conflict inside the parent `ScrollView`. Drag-to-reorder is handled via `.onMove`
    /// on a bare `ForEach`, which works on macOS 14+ inside a `LazyVStack`/`VStack`.
    /// Because bare ForEach `.onMove` requires a `List` container on macOS, we keep a
    /// `List` but give it a fixed computed height so it does not try to scroll independently.
    @ViewBuilder
    private func editableItemList(
        label: String? = nil,
        items: Binding<[EditableItem]>,
        placeholder: String,
        moveAction: @escaping (IndexSet, Int) -> Void,
        deleteAction: @escaping (IndexSet) -> Void,
        addAction: @escaping () -> Void
    ) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if let label {
                Text(label)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }

            // Fixed-height List prevents nested scroll fighting (issue 6).
            // Row height ≈ 34pt; add 8pt padding; minimum 50pt for empty state.
            let rowHeight: CGFloat = 34
            let listHeight = max(CGFloat(items.wrappedValue.count) * rowHeight + 8, 50)

            List {
                ForEach(items) { $item in
                    HStack(spacing: 8) {
                        Image(systemName: "line.3.horizontal")
                            .foregroundStyle(.tertiary)
                            .accessibilityHidden(true)
                        TextField(placeholder, text: $item.text, axis: .vertical)
                            .textFieldStyle(.plain)
                            .lineLimit(1...4)
                            .accessibilityLabel("\(label ?? "List") item text")
                    }
                    .padding(.vertical, 2)
                }
                .onMove(perform: moveAction)
                .onDelete(perform: deleteAction)
            }
            .listStyle(.plain)
            .frame(height: listHeight)

            Button {
                addAction()
            } label: {
                Label("Add Item", systemImage: "plus.circle")
            }
            .buttonStyle(.borderless)
        }
    }

    // MARK: - Raw JSON section

    private var rawJSONSection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 8) {
                DisclosureGroup {
                    VStack(alignment: .leading, spacing: 6) {
                        Label(
                            "Raw edits bypass field validation and may overwrite typed values on next load.",
                            systemImage: "exclamationmark.triangle"
                        )
                        .font(.caption)
                        .foregroundStyle(.orange)

                        TextEditor(text: $viewModel.rawJSON)
                            .font(.system(.body, design: .monospaced))
                            .frame(minHeight: 160)
                            .border(.separator, width: 1)
                            .accessibilityLabel("Raw JSON editor")
                    }
                } label: {
                    sectionHeader("Raw JSON")
                }
            }
            .padding(.vertical, 4)
        }
        .padding(.bottom, 12)
    }

    // MARK: - Footer

    private var footerSection: some View {
        HStack {
            if let error = viewModel.errorMessage {
                Label(error, systemImage: "exclamationmark.circle.fill")
                    .foregroundStyle(.red)
                    .font(.caption)
                    .accessibilityLabel("Error: \(error)")
            }
            Spacer()
            if viewModel.saved {
                Label("Saved", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                    .font(.caption)
            }
            Button("Save") {
                viewModel.triggerSave(client: appState.apiClient)
            }
            .buttonStyle(.borderedProminent)
            .disabled(!viewModel.isDirty || viewModel.isSaving)
            .accessibilityValue(viewModel.saved ? "Profile saved" : "")
        }
        .padding(.top, 4)
    }

    // MARK: - Helpers

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.headline)
            .padding(.bottom, 2)
    }
}

// MARK: - ProfileEditorViewModel

@MainActor @Observable
final class ProfileEditorViewModel {

    // MARK: Identity fields
    var role: String = "" { didSet { markDirty() } }
    var expertise: [ExpertiseEntry] = [] { didSet { markDirty() } }
    // Issue 2 fix: UUID-stable EditableItem instead of [String].
    var currentProjects: [EditableItem] = [] { didSet { markDirty() } }

    // MARK: Communication fields
    var tone: String? { didSet { markDirty() } }
    var detailLevel: String? { didSet { markDirty() } }
    var format: String? { didSet { markDirty() } }

    // MARK: Interests fields (issue 2 + 4 fix: EditableItem, not [String])
    var primaryInterests: [EditableItem] = [] { didSet { markDirty() } }
    var emergingInterests: [EditableItem] = [] { didSet { markDirty() } }

    // MARK: Reorderable list fields
    var opinions: [EditableItem] = [] { didSet { markDirty() } }
    var preferences: [EditableItem] = [] { didSet { markDirty() } }

    // MARK: Raw JSON escape hatch
    var rawJSON: String = "{}" { didSet { markDirty() } }

    // MARK: UI state
    var errorMessage: String?
    var saved: Bool = false
    var isDirty: Bool = false
    var isSaving: Bool = false

    // MARK: Private state
    private var originalProfile: Profile = Profile()
    private var originalJSON: String = "{}"
    private var isLoading: Bool = false
    // Issue 5 fix: store the active save task so rapid taps cancel the previous one.
    private var saveTask: Task<Void, Never>?

    // MARK: - Load

    func load(client: APIClient) async {
        isLoading = true
        defer { isLoading = false }
        do {
            let profile = try await client.getProfile()
            applyProfile(profile)
            snapshotOriginals(profile: profile)
            isDirty = false
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    // MARK: - Save (issue 5 fix: cancel previous task before starting a new one)

    /// Called from the view's Save button. Cancels any in-flight save before starting a new one.
    func triggerSave(client: APIClient) {
        saveTask?.cancel()
        saveTask = Task { await save(client: client) }
    }

    func save(client: APIClient) async {
        let validationError = validate()
        if let validationError {
            errorMessage = validationError
            return
        }

        isSaving = true
        defer { isSaving = false }

        do {
            let current = buildCurrentProfile()
            let patch = ProfilePatch.build(from: originalProfile, current: current)

            // If raw JSON changed but no typed fields changed, apply raw JSON as a patch.
            if rawJSON != originalJSON && current == originalProfile {
                guard let data = rawJSON.data(using: .utf8) else {
                    errorMessage = "Invalid JSON encoding"
                    return
                }
                let decoder = JSONDecoder()
                decoder.dateDecodingStrategy = .iso8601
                let parsedProfile = try decoder.decode(Profile.self, from: data)
                let rawPatch = ProfilePatch.build(from: originalProfile, current: parsedProfile)
                try await client.patchProfile(rawPatch)
            } else {
                try await client.patchProfile(patch)
            }

            guard !Task.isCancelled else { return }

            saved = true
            isDirty = false
            errorMessage = nil

            await load(client: client)

            guard !Task.isCancelled else { return }

            try? await Task.sleep(for: .seconds(2))
            saved = false
        } catch {
            if !Task.isCancelled {
                errorMessage = error.localizedDescription
            }
        }
    }

    // MARK: - Validation (issue 3 fix: role is optional; only validate length constraints)

    func validate() -> String? {
        for item in opinions where item.text.count > 500 {
            return "Opinion items must be 500 characters or fewer."
        }
        for item in preferences where item.text.count > 500 {
            return "Preference items must be 500 characters or fewer."
        }
        for entry in expertise where entry.skill.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return "Expertise skill names cannot be empty."
        }
        return nil
    }

    // MARK: - Mutation helpers (Identity)

    func addExpertise() {
        expertise.append(ExpertiseEntry())
    }

    func removeExpertise(_ entry: ExpertiseEntry) {
        expertise.removeAll { $0.id == entry.id }
    }

    func addCurrentProject() {
        currentProjects.append(EditableItem(text: ""))
    }

    func moveCurrentProjects(from source: IndexSet, to destination: Int) {
        currentProjects.move(fromOffsets: source, toOffset: destination)
    }

    func deleteCurrentProjects(at offsets: IndexSet) {
        currentProjects.remove(atOffsets: offsets)
    }

    // MARK: - Mutation helpers (Interests)

    func addPrimaryInterest() {
        primaryInterests.append(EditableItem(text: ""))
    }

    func movePrimaryInterests(from source: IndexSet, to destination: Int) {
        primaryInterests.move(fromOffsets: source, toOffset: destination)
    }

    func deletePrimaryInterests(at offsets: IndexSet) {
        primaryInterests.remove(atOffsets: offsets)
    }

    func addEmergingInterest() {
        emergingInterests.append(EditableItem(text: ""))
    }

    func moveEmergingInterests(from source: IndexSet, to destination: Int) {
        emergingInterests.move(fromOffsets: source, toOffset: destination)
    }

    func deleteEmergingInterests(at offsets: IndexSet) {
        emergingInterests.remove(atOffsets: offsets)
    }

    // MARK: - Mutation helpers (Opinions)

    func addOpinion() {
        opinions.append(EditableItem(text: ""))
    }

    func moveOpinions(from source: IndexSet, to destination: Int) {
        opinions.move(fromOffsets: source, toOffset: destination)
    }

    func deleteOpinions(at offsets: IndexSet) {
        opinions.remove(atOffsets: offsets)
    }

    // MARK: - Mutation helpers (Preferences)

    func addPreference() {
        preferences.append(EditableItem(text: ""))
    }

    func movePreferences(from source: IndexSet, to destination: Int) {
        preferences.move(fromOffsets: source, toOffset: destination)
    }

    func deletePreferences(at offsets: IndexSet) {
        preferences.remove(atOffsets: offsets)
    }

    // MARK: - Private helpers

    private func applyProfile(_ profile: Profile) {
        role = profile.identity.role ?? ""
        expertise = profile.identity.expertise.map { ExpertiseEntry(skill: $0.key, level: $0.value) }
            .sorted { $0.skill < $1.skill }

        // Issue 2 fix: wrap all [String] sources in EditableItem.
        // Issue 7 fix: preserve techStack and teamSize from original profile.
        currentProjects = (profile.identity.workingContext?.currentProjects ?? [])
            .map { EditableItem(text: $0) }

        tone = profile.communication.tone
        detailLevel = profile.communication.detailLevel
        format = profile.communication.format

        primaryInterests = profile.interests.primary.map { EditableItem(text: $0) }
        emergingInterests = profile.interests.emerging.map { EditableItem(text: $0) }

        opinions = profile.opinions.map { EditableItem(text: $0) }
        preferences = profile.preferences.map { EditableItem(text: $0) }

        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        if let data = try? encoder.encode(profile),
           let pretty = try? JSONSerialization.jsonObject(with: data),
           let prettyData = try? JSONSerialization.data(
               withJSONObject: pretty,
               options: [.prettyPrinted, .sortedKeys]
           ),
           let prettyString = String(data: prettyData, encoding: .utf8) {
            rawJSON = prettyString
        }
    }

    private func snapshotOriginals(profile: Profile) {
        originalProfile = profile
        originalJSON = rawJSON
    }

    private func buildCurrentProfile() -> Profile {
        var profile = Profile()

        let trimmedRole = role.trimmingCharacters(in: .whitespacesAndNewlines)
        profile.identity.role = trimmedRole.isEmpty ? nil : trimmedRole

        profile.identity.expertise = Dictionary(
            uniqueKeysWithValues: expertise
                .filter { !$0.skill.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
                .map { ($0.skill, $0.level) }
        )

        // Issue 7 fix: copy techStack and teamSize from the original snapshot so they are
        // not silently dropped when no editor fields exist for them yet.
        let filteredProjects = currentProjects
            .map(\.text)
            .filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        if !filteredProjects.isEmpty || originalProfile.identity.workingContext != nil {
            var wc = originalProfile.identity.workingContext ?? Profile.WorkingContext()
            wc.currentProjects = filteredProjects
            profile.identity.workingContext = wc
        }

        profile.communication.tone = tone
        profile.communication.detailLevel = detailLevel
        profile.communication.format = format

        profile.interests.primary = primaryInterests
            .map(\.text)
            .filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        profile.interests.emerging = emergingInterests
            .map(\.text)
            .filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

        // Fix 2: filter blank entries so empty rows don't reach the server.
        profile.opinions = opinions.map(\.text)
            .filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        profile.preferences = preferences.map(\.text)
            .filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

        // Preserve scalar fields the editor doesn't expose.
        profile.language = originalProfile.language
        profile.cloudModelPreference = originalProfile.cloudModelPreference

        return profile
    }

    private func markDirty() {
        guard !isLoading else { return }
        isDirty = true
    }
}
