import SwiftUI
import TBYDKit

/// The menubar status indicator showing the icon and current server status label.
struct StatusView: View {
    let appState: AppState

    var body: some View {
        Label(appState.statusLabel, systemImage: appState.statusIconName)
            .symbolRenderingMode(.monochrome)
            .foregroundStyle(appState.statusIconColor)
            .overlay(alignment: .topTrailing) {
                if appState.pendingDeltaCount > 0 {
                    Circle()
                        .fill(Color.red)
                        .frame(width: 7, height: 7)
                        .offset(x: 2, y: -2)
                        .accessibilityLabel("\(appState.pendingDeltaCount) pending profile delta\(appState.pendingDeltaCount == 1 ? "" : "s")")
                }
            }
    }
}
