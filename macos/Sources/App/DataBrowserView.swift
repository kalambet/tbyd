import SwiftUI
import TBYDKit

struct DataBrowserView: View {
    let appState: AppState
    @State private var viewModel = DataBrowserViewModel()

    var body: some View {
        NavigationSplitView {
            List(selection: $viewModel.selectedTab) {
                Label("Interactions", systemImage: "bubble.left.and.bubble.right")
                    .tag(DataBrowserViewModel.Tab.interactions)
                Label("Context Docs", systemImage: "doc.text")
                    .tag(DataBrowserViewModel.Tab.contextDocs)
            }
            .listStyle(.sidebar)
            .frame(minWidth: 160)
        } detail: {
            switch viewModel.selectedTab {
            case .interactions:
                InteractionsListView(viewModel: viewModel)
            case .contextDocs:
                ContextDocsListView(viewModel: viewModel)
            }
        }
        .navigationTitle("Data Browser")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                HStack(spacing: 12) {
                    if let stats = viewModel.stats {
                        Text(viewModel.statsLabel(stats))
                            .foregroundStyle(.secondary)
                            .font(.caption)
                    }
                    Button {
                        Task { await viewModel.refresh(client: appState.apiClient) }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                }
            }
        }
        .searchable(text: $viewModel.searchText, prompt: "Search...")
        .overlay(alignment: .bottom) {
            if let error = viewModel.errorMessage {
                Text(error)
                    .foregroundStyle(.red)
                    .font(.caption)
                    .padding(8)
                    .background(.red.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
                    .padding()
            }
        }
        .task {
            await viewModel.loadAll(client: appState.apiClient)
        }
    }
}

// MARK: - Interactions List

private struct InteractionsListView: View {
    let viewModel: DataBrowserViewModel
    @State private var deleteTarget: String?
    /// ID of the interaction whose active-thumb popover is open.
    @State private var notesTarget: String?
    /// Editable text inside the notes popover.
    @State private var notesText: String = ""

    var body: some View {
        if viewModel.filteredInteractions.isEmpty {
            ContentUnavailableView("No Interactions", systemImage: "bubble.left.and.bubble.right")
        } else {
            List(viewModel.filteredInteractions) { interaction in
                HStack(alignment: .center) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(interaction.query ?? "Untitled")
                            .font(.headline)
                            .lineLimit(1)
                        if let date = interaction.createdAt {
                            Text(date)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    Spacer()
                    FeedbackButtons(
                        interaction: interaction,
                        notesTarget: $notesTarget,
                        notesText: $notesText,
                        hasError: viewModel.feedbackError[interaction.id] == true,
                        onRate: { score in
                            // Preserve any existing notes when the user switches rating direction
                            // so that a direction change never silently erases a prior note.
                            Task { await viewModel.postFeedback(interactionId: interaction.id, score: score, notes: interaction.feedbackNotes) }
                        },
                        onSaveNotes: { score, notes in
                            Task { await viewModel.postFeedback(interactionId: interaction.id, score: score, notes: notes) }
                        }
                    )
                }
                .contextMenu {
                    Button("Delete", role: .destructive) {
                        deleteTarget = interaction.id
                    }
                }
            }
            .confirmationDialog(
                "Delete this interaction?",
                isPresented: Binding(
                    get: { deleteTarget != nil },
                    set: { if !$0 { deleteTarget = nil } }
                ),
                titleVisibility: .visible
            ) {
                Button("Delete", role: .destructive) {
                    if let id = deleteTarget {
                        Task { await viewModel.deleteInteraction(id: id) }
                    }
                }
            }
        }
    }
}

// MARK: - Feedback Buttons

private struct FeedbackButtons: View {
    let interaction: TBYDKit.APIClient.Interaction
    @Binding var notesTarget: String?
    @Binding var notesText: String
    let hasError: Bool
    let onRate: (Int) -> Void
    let onSaveNotes: (Int, String?) -> Void

    var body: some View {
        HStack(spacing: 4) {
            if hasError {
                Image(systemName: "exclamationmark.circle")
                    .foregroundStyle(.red)
                    .accessibilityLabel("Feedback error")
            }

            // Thumbs-up button
            Button {
                if interaction.isPositiveRated {
                    // Active — open popover to edit notes.
                    notesText = interaction.feedbackNotes ?? ""
                    notesTarget = interaction.id
                } else {
                    onRate(1)
                }
            } label: {
                Image(systemName: interaction.isPositiveRated ? "hand.thumbsup.fill" : "hand.thumbsup")
                    .foregroundStyle(interaction.isPositiveRated ? Color.accentColor : Color.secondary)
            }
            .buttonStyle(.borderless)
            .frame(minWidth: 28, minHeight: 28)
            .contentShape(Rectangle())
            .accessibilityLabel("Rate positive")
            .accessibilityValue(interaction.isPositiveRated ? "Selected" : "Not selected")
            .popover(
                isPresented: Binding(
                    get: { notesTarget == interaction.id && interaction.isPositiveRated },
                    set: { if !$0 { notesTarget = nil } }
                )
            ) {
                NotesPopover(
                    notesText: $notesText,
                    onSave: {
                        onSaveNotes(1, notesText.isEmpty ? nil : notesText)
                        notesTarget = nil
                    },
                    onDismiss: { notesTarget = nil }
                )
            }

            // Thumbs-down button
            Button {
                if interaction.isNegativeRated {
                    // Active — open popover to edit notes.
                    notesText = interaction.feedbackNotes ?? ""
                    notesTarget = interaction.id
                } else {
                    onRate(-1)
                }
            } label: {
                Image(systemName: interaction.isNegativeRated ? "hand.thumbsdown.fill" : "hand.thumbsdown")
                    .foregroundStyle(interaction.isNegativeRated ? Color.accentColor : Color.secondary)
            }
            .buttonStyle(.borderless)
            .frame(minWidth: 28, minHeight: 28)
            .contentShape(Rectangle())
            .accessibilityLabel("Rate negative")
            .accessibilityValue(interaction.isNegativeRated ? "Selected" : "Not selected")
            .popover(
                isPresented: Binding(
                    get: { notesTarget == interaction.id && interaction.isNegativeRated },
                    set: { if !$0 { notesTarget = nil } }
                )
            ) {
                NotesPopover(
                    notesText: $notesText,
                    onSave: {
                        onSaveNotes(-1, notesText.isEmpty ? nil : notesText)
                        notesTarget = nil
                    },
                    onDismiss: { notesTarget = nil }
                )
            }
        }
    }
}

// MARK: - Notes Popover

private struct NotesPopover: View {
    @Binding var notesText: String
    let onSave: () -> Void
    let onDismiss: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Feedback Notes")
                .font(.headline)
            TextField("Optional notes...", text: $notesText, axis: .vertical)
                .accessibilityLabel("Feedback notes")
                .lineLimit(3...6)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 240)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { onDismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Save") { onSave() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding()
    }
}

// MARK: - Context Docs List

private struct ContextDocsListView: View {
    let viewModel: DataBrowserViewModel
    @State private var deleteTarget: String?

    var body: some View {
        if viewModel.filteredContextDocs.isEmpty {
            ContentUnavailableView("No Documents", systemImage: "doc.text")
        } else {
            List(viewModel.filteredContextDocs) { doc in
                VStack(alignment: .leading, spacing: 4) {
                    Text(doc.title ?? doc.id)
                        .font(.headline)
                        .lineLimit(1)
                    HStack {
                        if let source = doc.source {
                            Text(source)
                                .font(.caption)
                                .padding(.horizontal, 4)
                                .padding(.vertical, 1)
                                .background(.quaternary)
                                .clipShape(RoundedRectangle(cornerRadius: 3))
                        }
                        if let tags = doc.tags, !tags.isEmpty {
                            Text(tags.joined(separator: ", "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                .contextMenu {
                    Button("Delete", role: .destructive) {
                        deleteTarget = doc.id
                    }
                }
            }
            .confirmationDialog(
                "Delete this document?",
                isPresented: Binding(
                    get: { deleteTarget != nil },
                    set: { if !$0 { deleteTarget = nil } }
                ),
                titleVisibility: .visible
            ) {
                Button("Delete", role: .destructive) {
                    if let id = deleteTarget {
                        Task { await viewModel.deleteContextDoc(id: id) }
                    }
                }
            }
        }
    }
}
