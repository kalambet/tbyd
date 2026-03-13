import SwiftUI
import TBYDKit

/// The menubar icon view for the tbyd status indicator.
struct StatusView: View {
    let appState: AppState

    var body: some View {
        Image(systemName: appState.statusIconName)
            .symbolRenderingMode(.monochrome)
            .foregroundStyle(appState.statusIconColor)
    }
}
