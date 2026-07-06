import Foundation

// Wire models shared with the `ezcloud` CLI (JSON contract) and the UI's
// in-memory editing row. Kept provider-agnostic on purpose: field keys are
// plain strings and all provider knowledge (labels, secrets, placeholders)
// arrives via ProviderSchema, so new backends need no model changes here.

struct ProfileSummary: Codable {
    let name: String
    let keys: [String]
}

struct ListResponse: Codable {
    let provider: String?
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
    /// Parser decisions the user must see (e.g. "private_key not imported").
    let notes: [String]?
}

struct SaveRequest: Codable {
    let fields: [String: String]
}

/// One provider backend as reported by `ezcloud providers`.
struct ProviderInfo: Codable {
    let id: String
    let displayName: String
    let canActivate: Bool
    let activateLabel: String?
}

/// One known field in a provider's schema (`ezcloud schema`). Bool/String
/// fields are optional because the CLI omits empty/false values.
struct FieldSpec: Codable {
    let key: String
    let display: String
    let env: String?
    let secret: Bool?
    let common: Bool?
    let placeholder: String?

    var isSecret: Bool { secret ?? false }
    var isCommon: Bool { common ?? false }
}

struct ProviderSchema: Codable {
    let provider: String
    let displayName: String
    let fields: [FieldSpec]

    private var byKey: [String: FieldSpec] {
        Dictionary(uniqueKeysWithValues: fields.map { ($0.key, $0) })
    }

    func spec(for key: String) -> FieldSpec? {
        byKey[key.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()]
    }
}

/// A workspace: a named group of (provider, profile) references, so one
/// person can keep clients/jobs/contexts separated. Never stores secrets.
struct WorkspaceMember: Codable, Equatable {
    let provider: String
    let profile: String
}

struct Workspace: Codable {
    var name: String
    var members: [WorkspaceMember]
}

/// One editable variable row in the Variables table.
struct FieldRow {
    var key: String
    var value: String
    /// Per-row secret reveal state (eye toggle). Non-secret rows ignore it.
    var revealed: Bool = false
}

// MARK: - Launch Templates (ezcloud lt … JSON contract)

struct LaunchTemplate: Codable {
    let id: String
    let name: String
    let defaultVersion: Int64
    let latestVersion: Int64
    let createdBy: String?
    let createTime: String?
}

struct LaunchTemplateVersion: Codable {
    let number: Int64
    let description: String?
    let isDefault: Bool
    let createTime: String?
}

struct LaunchTemplateData: Codable {
    let fields: [String: String]
}

struct ApplyResponse: Codable {
    let newVersion: Int64
    let setDefault: Bool
}
