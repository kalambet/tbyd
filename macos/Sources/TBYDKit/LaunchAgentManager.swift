import Foundation

/// Manages a LaunchAgent plist for auto-starting the menubar app at login.
public enum LaunchAgentManager: Sendable {
    private static let label = "com.tbyd.menubar"

    private static var plistURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/\(label).plist")
    }

    public static var isEnabled: Bool {
        FileManager.default.fileExists(atPath: plistURL.path)
    }

    public static func enable() throws {
        let appPath = Bundle.main.bundleURL.path

        let plist: [String: Any] = [
            "Label": label,
            "ProgramArguments": [appPath + "/Contents/MacOS/tbyd-menubar"],
            "RunAtLoad": true,
            "KeepAlive": false,
        ]

        let dir = plistURL.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)

        let data = try PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0)
        try data.write(to: plistURL)
    }

    public static func disable() throws {
        guard FileManager.default.fileExists(atPath: plistURL.path) else { return }
        try FileManager.default.removeItem(at: plistURL)
    }

    public static func setEnabled(_ enabled: Bool) throws {
        if enabled {
            try enable()
        } else {
            try disable()
        }
    }
}
