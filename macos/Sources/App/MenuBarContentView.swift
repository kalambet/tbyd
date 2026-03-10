import SwiftUI
import TBYDKit

struct MenuBarContentView: View {
    let appState: AppState
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Text(appState.statusLabel)
            .font(.headline)

        Divider()

        Button("Open Data Browser") {
            openWindow(id: "data-browser")
        }

        Button("Open Profile Editor") {
            openWindow(id: "profile-editor")
        }

        Divider()

        SettingsLink {
            Text("Preferences...")
        }

        if appState.isRunning {
            Button("Stop tbyd") {
                appState.stopServer()
            }
        } else {
            Button("Start tbyd") {
                appState.startServer()
            }
        }

        if let error = appState.errorMessage {
            Text(error)
                .foregroundStyle(.red)
                .font(.caption)
        }

        Divider()

        Button("Quit") {
            NSApplication.shared.terminate(nil)
        }
        .keyboardShortcut("q")
    }
}
