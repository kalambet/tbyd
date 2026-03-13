import SwiftUI
import TBYDKit

/// The menubar status indicator showing the icon and current server status label.
struct StatusView: View {
    let appState: AppState

    var body: some View {
        Label(appState.statusLabel, systemImage: appState.statusIconName)
            .symbolRenderingMode(.monochrome)
            .foregroundStyle(appState.statusIconColor)
    }
}
