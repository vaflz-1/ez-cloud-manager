import Foundation

// Wire models shared with the `ezcloud` CLI (JSON contract) and the UI's
// in-memory editing row. Kept provider-agnostic on purpose: field keys are
// plain strings and all provider knowledge (labels, secrets, placeholders)
// arrives via ProviderSchema, so new backends need no model changes here.

struct ProfileSummary: Codable {
    let name: String
    let keys: [String]
    let source: String?
    let readOnly: Bool?
    /// Marks the provider's own active/default entry (currently only gcloud
    /// configurations set this — see internal/provider.ProfileSummary.Active).
    /// Optional/omitted, not just false-by-default, so a provider that
    /// doesn't track this concept at all reads as "not applicable".
    let active: Bool?

    var isActive: Bool { active ?? false }
    var isReadOnly: Bool { readOnly ?? false }

    init(
        name: String,
        keys: [String],
        source: String? = nil,
        readOnly: Bool? = nil,
        active: Bool? = nil
    ) {
        self.name = name
        self.keys = keys
        self.source = source
        self.readOnly = readOnly
        self.active = active
    }
}

struct ListResponse: Codable {
    let provider: String?
    let path: String
    let profiles: [ProfileSummary]
}

/// One provider entry in the versioned, batched Connections snapshot. Errors
/// are per-provider so one unreadable store cannot hide every healthy one.
struct ConnectionProviderSnapshot: Codable {
    let provider: String
    let displayName: String
    let path: String?
    let profiles: [ProfileSummary]
    let error: String?
}

struct ConnectionsListResponse: Codable {
    let protocolVersion: Int
    let providers: [ConnectionProviderSnapshot]
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
    /// Existing-editor baseline. nil means the caller is using the legacy
    /// unconditional contract; an empty dictionary is a real empty profile.
    let expectedFields: [String: String]?
    /// New-connection precondition: fail if this name appeared since drafting.
    let expectAbsent: Bool
}

/// One provider backend as reported by `ezcloud providers`.
struct ProviderInfo: Codable {
    let id: String
    let displayName: String
    let canActivate: Bool
    let activateLabel: String?
    let canAuthenticate: Bool?
    let canSync: Bool?

    var supportsConnectionAuth: Bool {
        (canAuthenticate ?? false) && (canSync ?? false)
    }
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

/// First-window snapshot returned by `ezcloud app bootstrap`. The explicit
/// protocol version lets a newer shell reject an incompatible bundled core
/// cleanly instead of failing later on an ambiguous decode.
struct AppBootstrapResponse: Codable {
    let protocolVersion: Int
    let providers: [ProviderInfo]
    let schemas: [String: ProviderSchema]
    let profiles: [Profile]
    let activeProfile: Profile
    let addons: [PluginDescriptor]
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
/// Hashable so the Scope sheet (CloudAccountsWindowController+Scope.swift)
/// can track selection as a Set.
struct AccountRef: Codable, Equatable, Hashable {
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
/// UI for it, and targeted core/plugin mutations must preserve it so a later
/// window-state contribution can start using it without a data migration.
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

/// A global profile: the unit one app window binds to. Per
/// docs/PLATFORM.md principle 5 ("core owns no plugin data"), account
/// scoping is NOT a field here — it lives in `settings[PluginID.cloudAccounts]`
/// (see `cloudAccountsSettings` below), owned and edited by the Cloud
/// Accounts plugin itself.
struct Profile: Codable {
    var id: String
    var name: String
    var envVars: [EnvVar]
    /// P1 placeholder — always empty until the plugin host exists.
    var enabledPlugins: [String]
    /// Per-plugin settings blobs (P1.5): one opaque JSON value per plugin id.
    var settings: [String: JSONValue]?
    var windowState: JSONValue?
    var version: Int
    var createdAt: String
    /// Last explicit core-profile save. Addon/settings writes update
    /// `updatedAt` without changing this user-facing save timestamp.
    var savedAt: String
    var updatedAt: String
}

/// The Connections surface's settings shape, stored at the legacy
/// `Profile.settings[PluginID.cloudAccounts]` namespace for wire
/// compatibility. Mirrors internal/profile's CloudAccountsSettings; keep in
/// sync by hand. Only Connections decodes this namespace.
struct CloudAccountsSettings: Codable, Equatable {
    var showAllAccounts: Bool = false
    var accounts: [AccountRef] = []
}

extension Profile {
    /// Decodes this workspace's Connections visibility settings. A fresh
    /// workspace has no blob yet and must show every discovered connection;
    /// malformed optional UI state follows the same safe, usable default.
    var cloudAccountsSettings: CloudAccountsSettings {
        guard let raw = settings?[PluginID.cloudAccounts],
              let data = try? JSONEncoder().encode(raw),
              let decoded = try? JSONDecoder().decode(CloudAccountsSettings.self, from: data)
        else { return CloudAccountsSettings(showAllAccounts: true, accounts: []) }
        return decoded
    }
}

/// Native, locale-aware rendering for RFC3339 timestamps returned by the Go
/// core. Parsing failures remain visible as their original wire value.
enum AppTimestamp {
    private static let fractionalParser: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let standardParser = ISO8601DateFormatter()

    static func date(_ raw: String) -> Date? {
        fractionalParser.date(from: raw) ?? standardParser.date(from: raw)
    }

    static func display(_ raw: String) -> String {
        guard let date = date(raw) else { return raw }
        return DateFormatter.localizedString(from: date, dateStyle: .medium, timeStyle: .short)
    }
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

// MARK: - Test Connection (`ezcloud check`, P1.5)

/// The outcome of `ezcloud check` — a vendor-CLI identity/liveness call for
/// one credential-entry profile. `identity` is present only when `ok`;
/// `error` only when not. A provider that doesn't support Test Connection
/// yet (currently: azure) reports this same shape with ok=false, not a CLI
/// failure — the UI renders it identically to any other failed check.
struct CheckResult: Codable {
    let ok: Bool
    let identity: [String: String]?
    let error: String?
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
    let kind: String?      // "addon" | "system"; absent on legacy cores
    let enabled: Bool

    var isSystem: Bool { kind == "system" || id == PluginID.cloudAccounts }
}

/// Mirrors internal/plugin's exported ID constants — keep in sync by hand
/// (small, stable set; a mismatch just means a card silently doesn't open).
enum PluginID {
    static let cloudAccounts = "cloud-accounts"
    static let launchTemplates = "ec2-launch-templates"
    static let transfer = "transfer"
}
