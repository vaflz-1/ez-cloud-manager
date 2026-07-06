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

// MARK: - Global profiles (platform v2.0, docs/PLATFORM.md)
//
// A Profile is the new global container one app window binds to: cloud
// account references, env vars, enabled plugins and settings overrides.
// It supersedes the v1.1 Workspace/WorkspaceMember types (deleted from this
// file) — the wire contract now speaks Profile/AccountRef throughout.
//
// Naming: AccountRef references a per-provider credential entry by name
// (what the rest of the app calls a "profile" — ProfileSummary,
// ProfileResponse, --profile NAME). "Account" is used here specifically to
// avoid colliding with that older, unchanged meaning.

/// One (provider, account) credential-entry reference. Never stores secrets.
struct AccountRef: Codable, Equatable {
    let provider: String
    let account: String
}

/// One non-secret environment variable a profile applies to every `ezcloud`
/// CLI call made from its window (e.g. a default region).
struct EnvVar: Codable, Equatable {
    var key: String
    var value: String
}

/// A lossless wrapper for a JSON value this app doesn't interpret. Profile's
/// `windowState` is opaque here (Go stores it as json.RawMessage) — P0 has no
/// UI for it, but it must still round-trip untouched through the whole-object
/// PUT in `saveProfile`, so P1 can start reading/writing it with no data
/// migration.
enum JSONValue: Codable, Equatable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null; return }
        if let v = try? container.decode(Bool.self) { self = .bool(v); return }
        if let v = try? container.decode(Double.self) { self = .number(v); return }
        if let v = try? container.decode(String.self) { self = .string(v); return }
        if let v = try? container.decode([JSONValue].self) { self = .array(v); return }
        if let v = try? container.decode([String: JSONValue].self) { self = .object(v); return }
        throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value")
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null: try container.encodeNil()
        case .bool(let v): try container.encode(v)
        case .number(let v): try container.encode(v)
        case .string(let v): try container.encode(v)
        case .array(let v): try container.encode(v)
        case .object(let v): try container.encode(v)
        }
    }
}

/// A global profile: the unit one app window binds to.
struct Profile: Codable {
    var id: String
    var name: String
    /// Disables the Accounts filter (the window shows every account across
    /// every provider instead of only the referenced ones).
    var showAllAccounts: Bool
    var accounts: [AccountRef]
    var envVars: [EnvVar]
    /// P1 placeholder — always empty until the plugin host exists.
    var enabledPlugins: [String]
    /// P1+ overrides placeholder, unused by any UI in P0.
    var settings: [String: String]?
    var windowState: JSONValue?
    var version: Int
    var createdAt: String
    var updatedAt: String
}

/// The JSON shape for `ezcloud profile migrate`.
struct MigrateResponse: Codable {
    let migrated: Int
}

extension Array where Element == EnvVar {
    /// Last-key-wins collapse to the shape CredentialsService's `extraEnv`
    /// parameters expect.
    func asDictionary() -> [String: String] {
        var out: [String: String] = [:]
        for e in self { out[e.key] = e.value }
        return out
    }
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

// MARK: - Plugin host (platform v2.0, docs/PLATFORM.md phase P1)

/// A plugin as reported by `ezcloud plugins list --profile ID` — display
/// metadata plus this profile's enabled state. Contributes/Permissions are a
/// Tier-1 manifest concern the P1 hub never sees (built-ins are compiled in,
/// not loaded from a manifest file).
struct PluginDescriptor: Codable, Equatable {
    let id: String
    let name: String
    let description: String
    let icon: String       // SF Symbol name
    let clouds: [String]
    let category: String   // "DevOps" | "DevSecOps" | "AIOps"
    let enabled: Bool
}

/// Mirrors internal/plugin's exported ID constants — keep in sync by hand
/// (small, stable set; a mismatch just means a card silently doesn't open).
enum PluginID {
    static let cloudAccounts = "cloud-accounts"
    static let launchTemplates = "ec2-launch-templates"
    static let transfer = "transfer"
}
