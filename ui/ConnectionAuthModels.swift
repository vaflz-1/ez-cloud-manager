import Foundation

/// Safe, token-free wire models for connector-owned browser authentication
/// and connection synchronization. OAuth device codes, URLs, access tokens
/// and refresh tokens intentionally have no representation in this contract.
struct ConnectionAuthSnapshot: Codable {
    let protocolVersion: Int
    let provider: String
    let revision: String
    let candidates: [ConnectionAuthCandidate]
    let warnings: [String]
}

struct ConnectionAuthCandidate: Codable, Hashable {
    let id: String
    let name: String
    let displayName: String
    let sourceProfile: String?
    let authMode: String
    let principal: String?
    let accountID: String?
    let roleName: String?
    let projectID: String?
    let region: String?
    let status: String
    let canApply: Bool
    let reason: String?

    var targetDescription: String {
        if let projectID, !projectID.isEmpty { return projectID }
        if let accountID, !accountID.isEmpty { return accountID }
        return sourceProfile ?? name
    }

    var statusTitle: String {
        switch status {
        case "new": return "New"
        case "update": return "Update"
        case "unchanged": return "No changes"
        case "conflict": return "Conflict"
        default: return status.capitalized
        }
    }
}

struct ConnectionAuthLoginRequest: Codable {
    let expectedRevision: String?
    let candidateIDs: [String]
}

struct ConnectionAuthLoginResponse: Codable {
    let provider: String
    let ok: Bool
    let loggedIn: Int
    /// Login returns the discovery it authenticated against. This is required
    /// for GCP because its temporary, non-active auth configuration is removed
    /// before the core process exits; a later generic discovery must not fall
    /// back to a different globally-active account.
    let snapshot: ConnectionAuthSnapshot?
}

enum ConnectionAuthApplyMode: String, Codable, CaseIterable {
    case selected
    case updateAll = "update-all"
    case addNew = "add-new"

    var title: String {
        switch self {
        case .selected: return "Apply selected"
        case .updateAll: return "Update imported"
        case .addNew: return "Add new only"
        }
    }
}

struct ConnectionAuthApplyRequest: Codable {
    let expectedRevision: String
    let mode: ConnectionAuthApplyMode
    let candidateIDs: [String]
    /// Safe routing hint for GCP. The core verifies this principal is present
    /// in gcloud's credential store; it is never treated as a credential.
    let principal: String?
}

struct ConnectionAuthApplyItem: Codable {
    let candidateID: String
    let name: String
    let action: String
}

struct ConnectionAuthApplyResponse: Codable {
    let provider: String
    let revision: String
    let results: [ConnectionAuthApplyItem]
    let added: Int
    let updated: Int
    let unchanged: Int
}
