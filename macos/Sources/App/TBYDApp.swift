import SwiftUI
import TBYDKit

@main
struct TBYDApp: App {
    @State private var appState = AppState()

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView(appState: appState)
        } label: {
            Image(systemName: appState.statusIconName)
                .symbolRenderingMode(.monochrome)
                .foregroundStyle(appState.statusIconColor)
        }

        Window("Data Browser", id: "data-browser") {
            DataBrowserView(appState: appState)
                .frame(minWidth: 600, minHeight: 400)
        }

        Window("Profile Editor", id: "profile-editor") {
            ProfileEditorView(appState: appState)
                .frame(minWidth: 500, minHeight: 400)
        }

        Settings {
            PreferencesView(appState: appState)
                .frame(minWidth: 420, idealWidth: 450)
        }
    }
}
