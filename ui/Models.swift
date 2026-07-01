import Foundation

// Wire models shared with the `ezcloud` CLI (JSON contract) and the UI's
// in-memory editing row. Kept provider-agnostic on purpose: field keys are
// plain strings, so a future non-AWS backend needs no model changes here.

struct ProfileSummary: Codable {
    let name: String
    let keys: [String]
}

struct ListResponse: Codable {
    let path: String
    let profiles: [ProfileSummary]
}

struct ProfileResponse: Codable {
    let name: String
    let fields: [String: String]
}

struct ParseResponse: Codable {
    let profileName: String?
    let fields: [String: String]
}

struct SaveRequest: Codable {
    let fields: [String: String]
}

/// One editable variable row in the Variables table.
struct FieldRow {
    var key: String
    var value: String
    /// Per-row secret reveal state (eye toggle). Non-secret rows ignore it.
    var revealed: Bool = false
}
