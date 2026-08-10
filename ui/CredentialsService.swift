import Foundation

private final class PipeDrain: @unchecked Sendable {
    private let handle: FileHandle
    private let lock = NSLock()
    private var stored = Data()

    init(_ handle: FileHandle) {
        self.handle = handle
    }

    func start(in group: DispatchGroup) {
        group.enter()
        DispatchQueue.global(qos: .userInitiated).async { [self] in
            let value = handle.readDataToEndOfFile()
            lock.lock()
            stored = value
            lock.unlock()
            group.leave()
        }
    }

    func data() -> Data {
        lock.lock()
        defer { lock.unlock() }
        return stored
    }
}

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

        var errorDescription: String? {
            switch self {
            case .toolFailed(let message), .profileCoreConflict(let message):
                return message
            }
        }
    }

    private let toolPath: String
    private let snapshotLock = NSLock()
    private var profilesByID: [String: Profile] = [:]
    private var hasCompleteProfileSnapshot = false
    private var addonCatalogSnapshot: [PluginDescriptor]?

    init() {
        if let resource = Bundle.main.path(forResource: "ezcloud", ofType: nil) {
            toolPath = resource
        } else {
            // Dev fallback: resolve dist/ezcloud relative to this source file's
            // location (ui/CredentialsService.swift → project root), so the tool
            // path is not tied to any one developer's home directory.
            let projectRoot = URL(fileURLWithPath: #filePath)
                .deletingLastPathComponent()   // ui/
                .deletingLastPathComponent()   // project root
            toolPath = projectRoot.appendingPathComponent("dist/ezcloud").path
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

    func storeCompleteProfiles(_ profiles: [Profile]) {
        snapshotLock.lock()
        profilesByID = Dictionary(uniqueKeysWithValues: profiles.map { ($0.id, $0) })
        hasCompleteProfileSnapshot = true
        snapshotLock.unlock()
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
        snapshotLock.unlock()
    }

    func removeCachedProfile(id: String) {
        snapshotLock.lock()
        profilesByID.removeValue(forKey: id)
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

    func get(provider: String, _ name: String, extraEnv: [String: String] = [:]) throws -> ProfileResponse {
        try decode(run(["get", "--provider", provider, "--profile", name], input: nil, extraEnv: extraEnv))
    }

    func parse(provider: String, _ text: String, extraEnv: [String: String] = [:]) throws -> ParseResponse {
        try decode(run(["parse", "--provider", provider], input: text, extraEnv: extraEnv))
    }

    func delete(provider: String, _ name: String, extraEnv: [String: String] = [:]) throws {
        _ = try run(["delete", "--provider", provider, "--profile", name], input: nil, extraEnv: extraEnv)
    }

    func save(provider: String, _ name: String, fields: [String: String], extraEnv: [String: String] = [:]) throws {
        let payload = try JSONEncoder().encode(SaveRequest(fields: fields))
        _ = try run(["save", "--provider", provider, "--profile", name], inputData: payload, extraEnv: extraEnv)
    }

    /// Raw text export (env / dotenv / ini / json). The caller owns secret
    /// hygiene: use the concealed pasteboard type or a user-chosen file.
    func export(provider: String, _ name: String, format: String, extraEnv: [String: String] = [:]) throws -> String {
        let data = try run(["export", "--provider", provider, "--profile", name, "--format", format], input: nil, extraEnv: extraEnv)
        return String(data: data, encoding: .utf8) ?? ""
    }

    func activate(provider: String, _ name: String, extraEnv: [String: String] = [:]) throws {
        _ = try run(["activate", "--provider", provider, "--profile", name], input: nil, extraEnv: extraEnv)
    }

    /// Test Connection: runs the provider's own vendor-CLI identity/liveness
    /// call for one credential-entry profile. Always dispatch through
    /// `runAsync` — this hits the network via the vendor CLI, same as
    /// Launch Templates.
    func check(provider: String, _ name: String, extraEnv: [String: String] = [:]) throws -> CheckResult {
        try decode(run(["check", "--provider", provider, "--profile", name], input: nil, extraEnv: extraEnv))
    }

    // MARK: - Process plumbing

    func decode<T: Decodable>(_ data: Data) throws -> T {
        try JSONDecoder().decode(T.self, from: data)
    }

    func run(_ args: [String], input: String?, extraEnv: [String: String] = [:]) throws -> Data {
        try run(args, inputData: input?.data(using: .utf8), extraEnv: extraEnv)
    }

    func run(_ args: [String], inputData: Data?, extraEnv: [String: String] = [:]) throws -> Data {
        let process = Process()
        let output = Pipe()
        let errorPipe = Pipe()
        let input = Pipe()
        process.executableURL = URL(fileURLWithPath: toolPath)
        process.arguments = args
        process.standardOutput = output
        process.standardError = errorPipe
        process.standardInput = input
        process.environment = Self.childEnvironment(extraEnv: extraEnv)

        try process.run()
        let drains = DispatchGroup()
        let outputDrain = PipeDrain(output.fileHandleForReading)
        let errorDrain = PipeDrain(errorPipe.fileHandleForReading)
        outputDrain.start(in: drains)
        errorDrain.start(in: drains)
        if let inputData {
            input.fileHandleForWriting.write(inputData)
        }
        try? input.fileHandleForWriting.close()

        // Both pipes must drain concurrently. Reading stdout and then stderr
        // can deadlock when a failed child fills stderr before closing stdout.
        process.waitUntilExit()
        drains.wait()
        let data = outputDrain.data()
        let errorData = errorDrain.data()

        if process.terminationStatus != 0 {
            let message = String(data: errorData, encoding: .utf8).flatMap { $0.isEmpty ? nil : $0 }
                ?? String(data: data, encoding: .utf8) ?? "Command failed"
            let normalized = message.trimmingCharacters(in: .whitespacesAndNewlines)
            if normalized.contains("profile core changed since draft was loaded") {
                throw ServiceError.profileCoreConflict(normalized)
            }
            throw ServiceError.toolFailed(normalized)
        }
        return data
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

    /// Environment for the child CLI. Inherits the caller's environment (so
    /// AWS_SHARED_CREDENTIALS_FILE and the real HOME keep working for whichever
    /// user is running the app), then layers the owning window's profile env
    /// vars on top, then pins a safe, minimal PATH plus the Homebrew locations
    /// where the aws CLI is typically installed. Profiles can no longer store
    /// PATH (or any hijack var — internal/profile/validate.go hard-rejects them
    /// at Create/Save/Import), so pinning PATH last here is defense-in-depth
    /// for vendor-CLI discovery, not the primary guard.
    static func childEnvironment(extraEnv: [String: String] = [:]) -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        for (key, value) in extraEnv {
            env[key] = value
        }
        env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        return env
    }
}
