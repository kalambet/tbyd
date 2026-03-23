import SwiftUI
import TBYDKit

struct PendingDeltasView: View {
    let appState: AppState
    /// Called after an accept so the caller can reload the profile.
    var onAccepted: (() -> Void)?

    @State private var deltas: [APIClient.PendingDelta] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var inFlight: Set<String> = []
    @State private var deltaToReject: APIClient.PendingDelta?

    private static let dateFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateStyle = .medium
        f.timeStyle = .short
        return f
    }()

    var body: some View {
        VStack(spacing: 0) {
            if isLoading && deltas.isEmpty {
                ProgressView("Loading…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if deltas.isEmpty {
                emptyState
            } else {
                deltaList
            }

            if let error = errorMessage {
                HStack {
                    Label(error, systemImage: "exclamationmark.circle.fill")
                        .foregroundStyle(.red)
                        .font(.caption)
                        .accessibilityLabel("Error: \(error)")
                    Spacer()
                    Button("Dismiss") { errorMessage = nil }
                        .buttonStyle(.borderless)
                        .font(.caption)
                }
                .padding(.horizontal)
                .padding(.vertical, 6)
                .background(.red.opacity(0.08))
            }
        }
        .navigationTitle("Pending Profile Deltas")
        .task { await loadDeltas() }
        .confirmationDialog(
            "Reject Delta",
            isPresented: Binding(
                get: { deltaToReject != nil },
                set: { if !$0 { deltaToReject = nil } }
            ),
            presenting: deltaToReject
        ) { delta in
            Button("Reject", role: .destructive) {
                Task { await handleReject(delta) }
            }
            Button("Cancel", role: .cancel) {
                deltaToReject = nil
            }
        } message: { delta in
            Text("Are you sure you want to reject this delta? This action cannot be undone.\n\n\(delta.description)")
        }
    }

    // MARK: - Subviews

    private var emptyState: some View {
        VStack(spacing: 12) {
            Image(systemName: "checkmark.seal.fill")
                .font(.largeTitle)
                .imageScale(.large)
                .foregroundStyle(.secondary)
            Text("No Pending Deltas")
                .font(.headline)
            Text("Profile suggestions from nightly synthesis will appear here.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding()
        .accessibilityElement(children: .combine)
    }

    private var deltaList: some View {
        List(deltas) { delta in
            DeltaRow(
                delta: delta,
                isInFlight: inFlight.contains(delta.id),
                dateFormatter: Self.dateFormatter,
                onAccept: { await handleAccept(delta) },
                onRequestReject: { deltaToReject = delta }
            )
        }
        .listStyle(.inset)
    }

    // MARK: - Actions

    private func loadDeltas() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let all = try await appState.apiClient.listPendingDeltas()
            deltas = all.filter { $0.accepted == nil }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func handleAccept(_ delta: APIClient.PendingDelta) async {
        inFlight.insert(delta.id)
        defer { inFlight.remove(delta.id) }
        do {
            try await appState.apiClient.acceptDelta(id: delta.id)
            deltas.removeAll { $0.id == delta.id }
            await appState.refreshPendingDeltaCount()
            onAccepted?()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func handleReject(_ delta: APIClient.PendingDelta) async {
        deltaToReject = nil
        inFlight.insert(delta.id)
        defer { inFlight.remove(delta.id) }
        do {
            try await appState.apiClient.rejectDelta(id: delta.id)
            deltas.removeAll { $0.id == delta.id }
            await appState.refreshPendingDeltaCount()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

// MARK: - DeltaRow

private struct DeltaRow: View {
    let delta: APIClient.PendingDelta
    let isInFlight: Bool
    let dateFormatter: DateFormatter
    let onAccept: () async -> Void
    let onRequestReject: () -> Void

    @State private var isExpanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 8) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(delta.description)
                        .font(.body)

                    HStack(spacing: 6) {
                        sourceBadge
                        Text(dateFormatter.string(from: delta.createdAt))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                Spacer()

                if isInFlight {
                    ProgressView()
                        .controlSize(.small)
                } else {
                    HStack(spacing: 6) {
                        Button(role: .destructive) {
                            onRequestReject()
                        } label: {
                            Label("Reject", systemImage: "xmark.circle")
                        }
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Reject delta: \(delta.description)")

                        Button {
                            Task { await onAccept() }
                        } label: {
                            Label("Accept", systemImage: "checkmark.circle")
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(.green)
                        .accessibilityLabel("Accept delta: \(delta.description)")
                    }
                }
            }

            DisclosureGroup("Raw JSON", isExpanded: $isExpanded) {
                ScrollView(.horizontal, showsIndicators: false) {
                    Text(delta.deltaJSON)
                        .font(.system(.caption, design: .monospaced))
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .textSelection(.enabled)
                        .padding(.vertical, 4)
                }
                .frame(maxHeight: 160)
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .contain)
    }

    private var sourceBadge: some View {
        Text(delta.source)
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(.orange.opacity(0.15), in: Capsule())
            .foregroundStyle(.orange)
            .accessibilityLabel("Source: \(delta.source)")
    }
}
