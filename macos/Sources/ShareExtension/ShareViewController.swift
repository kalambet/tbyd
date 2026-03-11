import AppKit
import UniformTypeIdentifiers

/// macOS Share Extension view controller.
///
/// Implements `NSExtensionRequestHandling` and presents a compact panel that lets
/// the user add tags and an optional note before saving the shared content to the
/// tbyd local server via `POST /ingest`.
@MainActor
public final class ShareViewController: NSViewController {

    // MARK: - Constants

    static let largeFileSizeBytes: Int = 10 * 1024 * 1024   // 10 MB

    // MARK: - Injected dependencies (overridable in tests)

    /// Exposed so tests can inject a mock session.
    var ingestClient: IngestClient = IngestClient()

    // MARK: - UI elements

    private let titleLabel: NSTextField = {
        let f = NSTextField(labelWithString: "Save to tbyd")
        f.font = .boldSystemFont(ofSize: 14)
        f.translatesAutoresizingMaskIntoConstraints = false
        return f
    }()

    private let previewLabel: NSTextField = {
        let f = NSTextField(wrappingLabelWithString: "")
        f.font = .systemFont(ofSize: 12)
        f.textColor = .secondaryLabelColor
        f.maximumNumberOfLines = 3
        f.translatesAutoresizingMaskIntoConstraints = false
        return f
    }()

    // Internal so tests can drive the field directly via @testable import.
    let tagsField: NSTextField = {
        let f = NSTextField()
        f.placeholderString = "Tags (comma-separated, e.g. go,privacy)"
        f.font = .systemFont(ofSize: 12)
        f.translatesAutoresizingMaskIntoConstraints = false
        return f
    }()

    // Internal so tests can drive the field directly via @testable import.
    let noteField: NSTextField = {
        let f = NSTextField()
        f.placeholderString = "Optional note"
        f.font = .systemFont(ofSize: 12)
        f.translatesAutoresizingMaskIntoConstraints = false
        return f
    }()

    // Internal so tests can verify the error message via @testable import.
    let errorLabel: NSTextField = {
        let f = NSTextField(labelWithString: "")
        f.font = .systemFont(ofSize: 11)
        f.textColor = .systemRed
        f.isHidden = true
        f.maximumNumberOfLines = 3
        f.translatesAutoresizingMaskIntoConstraints = false
        return f
    }()

    private let saveButton: NSButton = {
        let b = NSButton(title: "Save to tbyd", target: nil, action: nil)
        b.bezelStyle = .push
        b.keyEquivalent = "\r"
        b.translatesAutoresizingMaskIntoConstraints = false
        return b
    }()

    private let cancelButton: NSButton = {
        let b = NSButton(title: "Cancel", target: nil, action: nil)
        b.bezelStyle = .push
        b.keyEquivalent = "\u{1b}"
        b.translatesAutoresizingMaskIntoConstraints = false
        return b
    }()

    private let progressIndicator: NSProgressIndicator = {
        let p = NSProgressIndicator()
        p.style = .spinning
        p.controlSize = .small
        p.isHidden = true
        p.translatesAutoresizingMaskIntoConstraints = false
        return p
    }()

    // MARK: - State

    /// Parsed tags from the tags field, split on commas and trimmed.
    var parsedTags: [String] {
        tagsField.stringValue
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    // MARK: - NSExtensionRequestHandling

    public override func beginRequest(with context: NSExtensionContext) {
        // The extension framework will have already set extensionContext before calling this.
        // We just kick off item loading; the view is already loaded via loadView().
        Task { [weak self] in
            guard let self else { return }
            await self.loadSharedItems(from: context)
        }
    }

    // MARK: - View lifecycle

    public override func loadView() {
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 360, height: 280))
        container.translatesAutoresizingMaskIntoConstraints = false
        self.view = container

        let buttonStack = NSStackView(views: [cancelButton, saveButton])
        buttonStack.orientation = .horizontal
        buttonStack.spacing = 8
        buttonStack.translatesAutoresizingMaskIntoConstraints = false

        let stack = NSStackView(views: [
            titleLabel,
            previewLabel,
            tagsField,
            noteField,
            errorLabel,
            progressIndicator,
            buttonStack,
        ])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 16, left: 16, bottom: 16, right: 16)
        stack.translatesAutoresizingMaskIntoConstraints = false

        container.addSubview(stack)

        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            stack.topAnchor.constraint(equalTo: container.topAnchor),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: container.bottomAnchor),

            tagsField.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            noteField.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            errorLabel.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            previewLabel.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            buttonStack.trailingAnchor.constraint(equalTo: stack.trailingAnchor, constant: -16),
        ])

        saveButton.target = self
        saveButton.action = #selector(saveTapped)
        cancelButton.target = self
        cancelButton.action = #selector(cancelTapped)
    }

    // MARK: - Actions

    @objc private func saveTapped() {
        setLoading(true)
        Task { [weak self] in
            guard let self else { return }
            await self.handleSave(context: self.extensionContext)
        }
    }

    @objc private func cancelTapped() {
        extensionContext?.cancelRequest(withError: NSError(
            domain: NSCocoaErrorDomain,
            code: NSUserCancelledError
        ))
    }

    // MARK: - Item loading

    /// Loads the first usable shared item and populates the preview label.
    /// Returns the resolved `SharedItem` for later use during save.
    /// Internal so the test target can inject a pre-resolved item.
    var resolvedItem: SharedItem?

    func loadSharedItems(from context: NSExtensionContext) async {
        let extensionItems = context.inputItems.compactMap { $0 as? NSExtensionItem }
        guard !extensionItems.isEmpty else {
            showError("No content was shared.")
            return
        }

        for extensionItem in extensionItems {
            guard let providers = extensionItem.attachments, !providers.isEmpty else { continue }
            for provider in providers {
                do {
                    let item = try await resolveItem(from: provider, extensionItem: extensionItem)
                    resolvedItem = item
                    previewLabel.stringValue = item.preview
                    return
                } catch {
                    continue  // try next provider
                }
            }
        }

        showError("No supported content was found in the shared items.")
    }

    /// Resolves an `NSItemProvider` into a `SharedItem`, checking for oversized files.
    func resolveItem(from provider: NSItemProvider, extensionItem: NSExtensionItem) async throws -> SharedItem {
        // 1. File URLs first — UTType.url also matches file URLs, so check the more
        //    specific type first to avoid ambiguity.
        if provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
            let fileItem = try await provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier)
            if let url = fileItem as? URL {
                return try await resolveFileURL(url, extensionItem: extensionItem)
            }
        }

        // 2. Web URLs — explicitly exclude file URLs to avoid overlap with the branch above.
        if provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
            let urlItem = try await provider.loadItem(forTypeIdentifier: UTType.url.identifier)
            if let url = urlItem as? URL, !url.isFileURL {
                let title = extensionItem.attributedTitle?.string ?? url.absoluteString
                return SharedItem(type: "url", preview: url.absoluteString, title: title, urlString: url.absoluteString)
            }
        }

        if provider.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
            let imageData: Data = try await withCheckedThrowingContinuation { continuation in
                provider.loadDataRepresentation(forTypeIdentifier: UTType.image.identifier) { data, error in
                    if let error = error {
                        continuation.resume(throwing: error)
                    } else if let data = data {
                        continuation.resume(returning: data)
                    } else {
                        continuation.resume(throwing: ShareError.unsupportedContentType)
                    }
                }
            }
            let base64 = imageData.base64EncodedString()
            return SharedItem(
                type: "file",
                preview: "image.png (\(imageData.count / 1024) KB)",
                title: "image.png",
                content: base64,
                metadata: [
                    "filename": "image.png",
                    "mime_type": "image/png",
                    "byte_count": String(imageData.count),
                ]
            )
        }

        if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
            let textItem = try await provider.loadItem(forTypeIdentifier: UTType.plainText.identifier)
            if let text = textItem as? String {
                let title = extensionItem.attributedTitle?.string ?? String(text.prefix(60))
                let snippet = String(text.prefix(200))
                return SharedItem(type: "text", preview: snippet, title: title, content: text)
            }
        }

        throw ShareError.unsupportedContentType
    }

    /// Streams base64 encoding in fixed-size chunks to avoid the ~3x memory spike
    /// that results from loading the entire file into `Data` at once.
    /// `chunkSize` must be a multiple of 3 so each chunk encodes cleanly without padding.
    private func base64EncodedContents(of url: URL) throws -> String {
        let chunkSize = 3 * 1024  // 3 KB — multiple of 3 for valid base64 boundaries
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }

        var parts: [String] = []
        while true {
            guard let chunk = try handle.read(upToCount: chunkSize), !chunk.isEmpty else { break }
            parts.append(chunk.base64EncodedString())
        }
        return parts.joined()
    }

    private func resolveFileURL(_ url: URL, extensionItem: NSExtensionItem) async throws -> SharedItem {
        let attrs = try FileManager.default.attributesOfItem(atPath: url.path())
        let fileSize = (attrs[.size] as? Int) ?? 0

        if fileSize > Self.largeFileSizeBytes {
            return SharedItem(
                type: "file",
                preview: "\(url.lastPathComponent) (\(fileSize / (1024 * 1024)) MB)",
                title: url.lastPathComponent,
                fileURL: url,
                isOversized: true
            )
        }

        let base64 = try base64EncodedContents(of: url)
        let title = extensionItem.attributedTitle?.string ?? url.lastPathComponent
        let mimeType = UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
        return SharedItem(
            type: "file",
            preview: "\(url.lastPathComponent) (\(fileSize / 1024) KB)",
            title: title,
            content: base64,
            fileURL: url,
            metadata: [
                "filename": url.lastPathComponent,
                "extension": url.pathExtension,
                "mime_type": mimeType,
                "byte_count": String(fileSize),
            ]
        )
    }

    // MARK: - Save

    /// Internal entry point for tests — bypasses `extensionContext` completion.
    func handleSave() async {
        await handleSave(context: nil)
    }

    func handleSave(context: NSExtensionContext?) async {
        guard let item = resolvedItem else {
            setLoading(false)
            showError("No content to save.")
            return
        }

        if item.isOversized {
            setLoading(false)
            showError("File is larger than 10 MB. Use the tbyd CLI to ingest large files.")
            return
        }

        let tags = parsedTags
        let note = noteField.stringValue.isEmpty ? nil : noteField.stringValue

        var metadata = item.metadata
        if let note {
            metadata["note"] = note
        }

        let request = IngestRequest(
            type: item.type,
            title: item.title,
            content: item.content,
            url: item.urlString,
            tags: tags,
            metadata: metadata
        )

        do {
            try await ingestClient.post(request)
            setLoading(false)
            context?.completeRequest(returningItems: [], completionHandler: nil)
        } catch let error as URLError where
            [.cannotConnectToHost, .networkConnectionLost, .notConnectedToInternet, .timedOut].contains(error.code) {
            setLoading(false)
            showError("tbyd is not running. Start it from the menubar.")
        } catch {
            setLoading(false)
            showError("Failed to save: \(error.localizedDescription)")
        }
    }

    // MARK: - UI helpers

    func showError(_ message: String) {
        errorLabel.stringValue = message
        errorLabel.isHidden = false
    }

    private func setLoading(_ loading: Bool) {
        saveButton.isEnabled = !loading
        if loading {
            errorLabel.isHidden = true
            errorLabel.stringValue = ""
            progressIndicator.isHidden = false
            progressIndicator.startAnimation(nil)
        } else {
            progressIndicator.stopAnimation(nil)
            progressIndicator.isHidden = true
        }
    }
}

// MARK: - Supporting types

/// A resolved shared item ready to be sent to the ingest endpoint.
struct SharedItem: Sendable {
    let type: String            // "text", "url", "file"
    let preview: String         // shown in the UI preview label
    let title: String?
    let content: String?        // plain text or base64-encoded file data
    let urlString: String?
    let fileURL: URL?
    let isOversized: Bool
    let metadata: [String: String]

    init(
        type: String,
        preview: String,
        title: String? = nil,
        content: String? = nil,
        urlString: String? = nil,
        fileURL: URL? = nil,
        isOversized: Bool = false,
        metadata: [String: String] = [:]
    ) {
        self.type = type
        self.preview = preview
        self.title = title
        self.content = content
        self.urlString = urlString
        self.fileURL = fileURL
        self.isOversized = isOversized
        self.metadata = metadata
    }
}

enum ShareError: Error, LocalizedError {
    case unsupportedContentType

    var errorDescription: String? {
        switch self {
        case .unsupportedContentType:
            return "The shared content type is not supported."
        }
    }
}
