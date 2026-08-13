import Foundation

/// CredentialsService is the single boundary between the UI and the `ezcloud`
/// CLI. All subprocess spawning, stdin/stdout piping, and JSON decoding lives
/// here, so the rest of the app deals only in typed models.
///
/// Every profile operation takes a provider id ("aws", "gcp", "azure"); the
/// CLI defaults to aws so the original single-cloud contract is preserved.
final class CredentialsService {
    enum ServiceError: LocalizedError {
        case toolFailed(String)
        /// Stable Swift-side classification of the Go core's optimistic
        /// concurrency failure. UI code must not infer conflict behavior from
        /// an arbitrary alert string.
        case profileCoreConflict(String)
        case profileSettingsConflict(String)
        case connectionConflict(String)
        case connectionDeletedScopeCleanupFailed(String)

        var errorDescription: String? {
            switch self {
            case .toolFailed(let message),
                 .profileCoreConflict(let message),
                 .profileSettingsConflict(let message),
                 .connectionConflict(let message),
                 .connectionDeletedScopeCleanupFailed(let message):
                return message
            }
        }
    }

    private let toolPath: String
    private let snapshotLock = NSLock()
    private var profilesByID: [String: Profile] = [:]
    private var hasCompleteProfileSnapshot = false
    private var addonCatalogSnapshot: [PluginDescriptor]?
    private var profileSnapshotRevision: UInt64 = 0

    init(toolPath explicitToolPath: String? = nil) {
        if let explicitToolPath {
            // Dependency injection is the only development override. Keeping
            // source-relative fallbacks out of production also prevents the
            // compiler from embedding a developer's absolute checkout path in
            // the public Mach-O binary.
            toolPath = explicitToolPath
        } else if let resource = Bundle.main.path(forResource: "ezcloud", ofType: nil) {
            toolPath = resource
        } else {
            // A production bundle without its signed core is corrupt. Point at
            // the expected absolute resource location so posix_spawn returns a
            // precise ENOENT error instead of searching PATH or guessing a
            // developer-specific checkout.
            let resources = Bundle.main.resourceURL ?? URL(fileURLWithPath: "/")
            toolPath = resources.appendingPathComponent("ezcloud").path
        }
    }

    // MARK: - App-scoped snapshots

    func storeBootstrapSnapshot(_ response: AppBootstrapResponse) {
        snapshotLock.lock()
        profilesByID = Dictionary(uniqueKeysWithValues: response.profiles.map { ($0.id, $0) })
        hasCompleteProfileSnapshot = true
        addonCatalogSnapshot = response.addons.map {
            PluginDescriptor(
                id: $0.id,
                name: $0.name,
                description: $0.description,
                icon: $0.icon,
                clouds: $0.clouds,
                category: $0.category,
                kind: $0.kind,
                enabled: false
            )
        }
        profileSnapshotRevision &+= 1
        snapshotLock.unlock()
    }

    func cachedProfiles() -> [Profile]? {
        snapshotLock.lock()
        defer { snapshotLock.unlock() }
        guard hasCompleteProfileSnapshot else { return nil }
        return profilesByID.values.sorted {
            $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
    }

    func cachedProfile(id: String) -> Profile? {
        snapshotLock.lock()
        defer { snapshotLock.unlock() }
        return profilesByID[id]
    }

    /// Captures the in-memory Workspace revision immediately before a fresh
    /// disk read. A later commit is accepted only if no mutation updated the
    /// shared snapshot while that I/O was in flight.
    func beginCompleteProfileRead() -> UInt64 {
        snapshotLock.lock()
        defer { snapshotLock.unlock() }
        return profileSnapshotRevision
    }

    func isCompleteProfileReadCurrent(_ revision: UInt64) -> Bool {
        snapshotLock.lock()
        defer { snapshotLock.unlock() }
        return profileSnapshotRevision == revision
    }

    /// Atomically commits a complete disk snapshot if it is still based on the
    /// current in-memory revision. Returning the accepted, sorted snapshot is
    /// important: callers must render this value, never the unguarded raw read.
    func commitCompleteProfiles(
        _ profiles: [Profile],
        ifUnchangedSince expectedRevision: UInt64
    ) -> [Profile]? {
        snapshotLock.lock()
        defer { snapshotLock.unlock() }
        guard profileSnapshotRevision == expectedRevision else { return nil }
        profilesByID = Dictionary(uniqueKeysWithValues: profiles.map { ($0.id, $0) })
        hasCompleteProfileSnapshot = true
        profileSnapshotRevision &+= 1
        return profilesByID.values.sorted {
            $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
    }

    func storeProfile(_ profile: Profile) {
        snapshotLock.lock()
        if let current = profilesByID[profile.id],
           let currentDate = AppTimestamp.date(current.updatedAt),
           let incomingDate = AppTimestamp.date(profile.updatedAt),
           incomingDate < currentDate {
            snapshotLock.unlock()
            return
        }
        profilesByID[profile.id] = profile
        profileSnapshotRevision &+= 1
        snapshotLock.unlock()
    }

    func removeCachedProfile(id: String) {
        snapshotLock.lock()
        if profilesByID.removeValue(forKey: id) != nil {
            profileSnapshotRevision &+= 1
        }
        snapshotLock.unlock()
    }

    func cachedPlugins(profileID: String) -> [PluginDescriptor]? {
        snapshotLock.lock()
        defer { snapshotLock.unlock() }
        guard let catalog = addonCatalogSnapshot,
              let profile = profilesByID[profileID]
        else { return nil }
        let enabled = Set(profile.enabledPlugins)
        return catalog.map {
            PluginDescriptor(
                id: $0.id,
                name: $0.name,
                description: $0.description,
                icon: $0.icon,
                clouds: $0.clouds,
                category: $0.category,
                kind: $0.kind,
                enabled: enabled.contains($0.id)
            )
        }
    }

    func storePluginSnapshot(_ descriptors: [PluginDescriptor], profileID: String) {
        snapshotLock.lock()
        addonCatalogSnapshot = descriptors.map {
            PluginDescriptor(
                id: $0.id,
                name: $0.name,
                description: $0.description,
                icon: $0.icon,
                clouds: $0.clouds,
                category: $0.category,
                kind: $0.kind,
                enabled: false
            )
        }
        if var profile = profilesByID[profileID] {
            profile.enabledPlugins = descriptors.filter(\.enabled).map(\.id)
            profilesByID[profileID] = profile
            profileSnapshotRevision &+= 1
        }
        snapshotLock.unlock()
    }

    // MARK: - Providers & schemas

    func providers() throws -> [ProviderInfo] {
        try decode(run(["providers"], input: nil))
    }

    func schema(provider: String) throws -> ProviderSchema {
        try decode(run(["schema", "--provider", provider], input: nil))
    }

    // MARK: - Profile operations
    //
    // Every call below takes an `extraEnv` the caller resolves from the
    // owning window's Profile (`profile.envVars.asDictionary()`) — this is
    // what makes two windows on two profiles behave with independent env
    // (e.g. a different AWS_SHARED_CREDENTIALS_FILE or default region),
    // not just independent Accounts filtering. It defaults to empty so
    // callers with no profile context (tests, tools) are unaffected.

    func list(provider: String, extraEnv: [String: String] = [:]) throws -> ListResponse {
        try decode(run(["list", "--provider", provider], input: nil, extraEnv: extraEnv))
    }

    func get(
        provider: String,
        _ name: String,
        workspaceID: String,
        extraEnv: [String: String] = [:]
    ) throws -> ProfileResponse {
        try decode(run(
            ["get", "--provider", provider, "--profile", name, "--workspace", workspaceID],
            input: nil,
            extraEnv: extraEnv
        ))
    }

    func parse(provider: String, _ text: String, extraEnv: [String: String] = [:]) throws -> ParseResponse {
        try decode(run(["parse", "--provider", provider], input: text, extraEnv: extraEnv))
    }

    func delete(
        provider: String,
        _ name: String,
        workspaceID: String,
        extraEnv: [String: String] = [:]
    ) throws {
        _ = try run(
            ["delete", "--provider", provider, "--profile", name, "--workspace", workspaceID],
            input: nil,
            extraEnv: extraEnv
        )
    }

    func save(
        provider: String,
        _ name: String,
        workspaceID: String,
        fields: [String: String],
        expectedFields: [String: String]? = nil,
        expectAbsent: Bool = false,
        extraEnv: [String: String] = [:]
    ) throws {
        let payload = try JSONEncoder().encode(SaveRequest(
            fields: fields,
            expectedFields: expectedFields,
            expectAbsent: expectAbsent
        ))
        _ = try run(
            ["save", "--provider", provider, "--profile", name, "--workspace", workspaceID],
            inputData: payload,
            extraEnv: extraEnv
        )
    }

    /// Raw text export (env / dotenv / ini / json). The caller owns secret
    /// hygiene: use the concealed pasteboard type or a user-chosen file.
    func export(
        provider: String,
        _ name: String,
        workspaceID: String,
        format: String,
        extraEnv: [String: String] = [:]
    ) throws -> String {
        let data = try run(
            ["export", "--provider", provider, "--profile", name, "--workspace", workspaceID, "--format", format],
            input: nil,
            extraEnv: extraEnv
        )
        return String(data: data, encoding: .utf8) ?? ""
    }

    func activate(
        provider: String,
        _ name: String,
        workspaceID: String,
        extraEnv: [String: String] = [:]
    ) throws {
        _ = try run(
            ["activate", "--provider", provider, "--profile", name, "--workspace", workspaceID],
            input: nil,
            extraEnv: extraEnv
        )
    }

    /// Test Connection: runs the provider's own vendor-CLI identity/liveness
    /// call for one credential-entry profile. Always dispatch through
    /// `runAsync` — this hits the network via the vendor CLI, same as
    /// Launch Templates.
    func check(
        provider: String,
        _ name: String,
        workspaceID: String,
        extraEnv: [String: String] = [:]
    ) throws -> CheckResult {
        try decode(run(
            ["check", "--provider", provider, "--profile", name, "--workspace", workspaceID],
            input: nil,
            extraEnv: extraEnv
        ))
    }

    // MARK: - Process plumbing

    func decode<T: Decodable>(_ data: Data) throws -> T {
        try JSONDecoder().decode(T.self, from: data)
    }

    func run(
        _ args: [String],
        input: String?,
        extraEnv: [String: String] = [:],
        cancellation: FastProcessCancellation? = nil
    ) throws -> Data {
        try run(
            args,
            inputData: input?.data(using: .utf8),
            extraEnv: extraEnv,
            cancellation: cancellation
        )
    }

    func run(
        _ args: [String],
        inputData: Data?,
        extraEnv: [String: String] = [:],
        cancellation: FastProcessCancellation? = nil
    ) throws -> Data {
        let result = try FastProcessRunner.run(
            executable: toolPath,
            arguments: args,
            input: inputData,
            environment: Self.childEnvironment(extraEnv: extraEnv),
            cancellation: cancellation
        )
        if result.terminationStatus != 0 {
            let data = result.standardOutput
            let errorData = result.standardError
            let fallback = result.terminationSignal.map {
                "Bundled core terminated by signal \($0)"
            } ?? "Command failed"
            let message = String(data: errorData, encoding: .utf8).flatMap {
                $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0
            }
                ?? String(data: data, encoding: .utf8).flatMap {
                    $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0
                }
                ?? fallback
            let normalized = message.trimmingCharacters(in: .whitespacesAndNewlines)
            if normalized.contains("EZCLOUD_CONNECTION_CONFLICT") {
                throw ServiceError.connectionConflict(normalized)
            }
            if normalized.contains("EZCLOUD_PROFILE_SETTINGS_CONFLICT") {
                throw ServiceError.profileSettingsConflict(normalized)
            }
            if normalized.contains("EZCLOUD_CONNECTION_DELETED_SCOPE_CLEANUP_FAILED") {
                throw ServiceError.connectionDeletedScopeCleanupFailed(normalized)
            }
            if normalized.contains("profile core changed since draft was loaded") {
                throw ServiceError.profileCoreConflict(normalized)
            }
            throw ServiceError.toolFailed(normalized)
        }
        return result.standardOutput
    }

    /// Runs a CLI call off the main thread and delivers the typed result back
    /// on it — for operations that hit the network (Launch Templates) and
    /// must not beachball the UI.
    func runAsync<T>(_ work: @escaping () throws -> T, completion: @escaping (Result<T, Error>) -> Void) {
        DispatchQueue.global(qos: .userInitiated).async {
            let result = Result { try work() }
            DispatchQueue.main.async { completion(result) }
        }
    }

    /// Environment for the child CLI. The app must not forward its entire
    /// ambient environment: when launched from a terminal that can include
    /// credentials, proxy/CA overrides and interpreter injection knobs which
    /// would then reach vendor CLIs. Start with the small set needed for normal
    /// user paths, temporary files and locale behavior, layer the validated
    /// Workspace context on top, and pin executable discovery last.
    static func childEnvironment(extraEnv: [String: String] = [:]) -> [String: String] {
        let ambient = ProcessInfo.processInfo.environment
        let inheritedKeys = [
            "HOME", "USER", "LOGNAME", "TMPDIR",
            "LANG", "LC_ALL", "LC_CTYPE", "TZ",
            "SSH_AUTH_SOCK", "__CF_USER_TEXT_ENCODING"
        ]
        var env = Dictionary(
            uniqueKeysWithValues: inheritedKeys.compactMap { key in
                ambient[key].map { (key, $0) }
            }
        )
        env["HOME"] = env["HOME"] ?? FileManager.default.homeDirectoryForCurrentUser.path
        env["TMPDIR"] = env["TMPDIR"] ?? NSTemporaryDirectory()
        for (key, value) in extraEnv {
            env[key] = value
        }
        env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        return env
    }
}
