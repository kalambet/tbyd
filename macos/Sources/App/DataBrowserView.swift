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
        .task {
            await viewModel.loadAll(client: appState.apiClient)
        }
    }
}

// MARK: - Interactions List

private struct InteractionsListView: View {
    let viewModel: DataBrowserViewModel
    @State private var deleteTarget: String?

    var body: some View {
        if viewModel.filteredInteractions.isEmpty {
            ContentUnavailableView("No Interactions", systemImage: "bubble.left.and.bubble.right")
        } else {
            List(viewModel.filteredInteractions) { interaction in
                VStack(alignment: .leading, spacing: 4) {
                    Text(interaction.summary ?? interaction.query ?? "Untitled")
                        .font(.headline)
                        .lineLimit(1)
                    if let date = interaction.createdAt {
                        Text(date)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
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
