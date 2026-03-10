import Foundation
import Security

/// Provides read/write access to the macOS Keychain for tbyd secrets.
public enum KeychainService: Sendable {
    public static let defaultService = "tbyd"

    public enum Account: String, Sendable {
        case apiToken = "tbyd-api-token"
        case openRouterAPIKey = "openrouter_api_key"
    }

    public static func get(_ account: Account, service: String = defaultService) throws -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account.rawValue,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]

        var result: AnyObject?
        let status = SecItemCopyMatching(query as CFDictionary, &result)

        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess else {
            throw KeychainError.unhandled(status)
        }
        guard let data = result as? Data, let value = String(data: data, encoding: .utf8) else {
            return nil
        }
        return value
    }

    public static func set(_ account: Account, value: String, service: String = defaultService) throws {
        guard let data = value.data(using: .utf8) else { return }

        // Try to update first.
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account.rawValue,
        ]
        let update: [String: Any] = [
            kSecValueData as String: data,
        ]

        let updateStatus = SecItemUpdate(query as CFDictionary, update as CFDictionary)
        if updateStatus == errSecSuccess { return }

        // Item doesn't exist — add it.
        var addQuery = query
        addQuery[kSecValueData as String] = data
        addQuery[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        let addStatus = SecItemAdd(addQuery as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw KeychainError.unhandled(addStatus)
        }
    }

    public static func delete(_ account: Account, service: String = defaultService) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account.rawValue,
        ]
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.unhandled(status)
        }
    }
}

public enum KeychainError: Error, LocalizedError {
    case unhandled(OSStatus)

    public var errorDescription: String? {
        switch self {
        case .unhandled(let status):
            return "Keychain error: \(SecCopyErrorMessageString(status, nil) as String? ?? "unknown (\(status))")"
        }
    }
}
