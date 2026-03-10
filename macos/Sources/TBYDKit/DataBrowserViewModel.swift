import Foundation

/// View model for the data browser, managing interactions and context docs.
@MainActor @Observable
public final class DataBrowserViewModel {
    public enum Tab: Hashable, Sendable {
        case interactions
        case contextDocs
    }

    public struct Stats: Sendable {
        public var totalDocs: Int
        public var totalInteractions: Int
        public var docsAtLimit: Bool
        public var interactionsAtLimit: Bool

        public init(totalDocs: Int, totalInteractions: Int, docsAtLimit: Bool = false, interactionsAtLimit: Bool = false) {
            self.totalDocs = totalDocs
            self.totalInteractions = totalInteractions
            self.docsAtLimit = docsAtLimit
            self.interactionsAtLimit = interactionsAtLimit
        }
    }

    public var selectedTab: Tab = .interactions
    public var interactions: [APIClient.Interaction] = []
    public var contextDocs: [APIClient.ContextDoc] = []
    public var searchText: String = ""
    public var stats: Stats?
    public var errorMessage: String?

    private static let fetchLimit = 500
    private var client: APIClient?

    public init() {}

    public var filteredInteractions: [APIClient.Interaction] {
        guard !searchText.isEmpty else { return interactions }
        return interactions.filter { interaction in
            (interaction.summary ?? "").localizedCaseInsensitiveContains(searchText)
                || (interaction.query ?? "").localizedCaseInsensitiveContains(searchText)
        }
    }

    public var filteredContextDocs: [APIClient.ContextDoc] {
        guard !searchText.isEmpty else { return contextDocs }
        return contextDocs.filter { doc in
            (doc.title ?? "").localizedCaseInsensitiveContains(searchText)
                || (doc.tags ?? []).joined(separator: " ").localizedCaseInsensitiveContains(searchText)
        }
    }

    public func statsLabel(_ stats: Stats) -> String {
        let docsLabel = stats.docsAtLimit ? "\(stats.totalDocs)+" : "\(stats.totalDocs)"
        let interLabel = stats.interactionsAtLimit ? "\(stats.totalInteractions)+" : "\(stats.totalInteractions)"
        return String(
            localized: "\(docsLabel) docs, \(interLabel) interactions",
            comment: "Data browser status bar showing count of documents and interactions"
        )
    }

    public func loadAll(client: APIClient) async {
        self.client = client
        await refresh(client: client)
    }

    public func refresh(client: APIClient) async {
        let limit = Self.fetchLimit
        do {
            async let fetchedInteractions = client.listInteractions(limit: limit)
            async let fetchedDocs = client.listContextDocs(limit: limit)
            interactions = try await fetchedInteractions
            contextDocs = try await fetchedDocs
            stats = Stats(
                totalDocs: contextDocs.count,
                totalInteractions: interactions.count,
                docsAtLimit: contextDocs.count >= limit,
                interactionsAtLimit: interactions.count >= limit
            )
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    public func deleteInteraction(id: String) async {
        guard let client else { return }
        do {
            try await client.deleteInteraction(id: id)
            interactions.removeAll { $0.id == id }
            stats?.totalInteractions = interactions.count
            stats?.interactionsAtLimit = interactions.count >= Self.fetchLimit
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    public func deleteContextDoc(id: String) async {
        guard let client else { return }
        do {
            try await client.deleteContextDoc(id: id)
            contextDocs.removeAll { $0.id == id }
            stats?.totalDocs = contextDocs.count
            stats?.docsAtLimit = contextDocs.count >= Self.fetchLimit
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
