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

        var errorDescription: String? {
            switch self {
            case .toolFailed(let message):
                return message
            }
        }
    }

    private let toolPath: String

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

    // MARK: - Providers & schemas

    func providers() throws -> [ProviderInfo] {
        try decode(run(["providers"], input: nil))
    }

    func schema(provider: String) throws -> ProviderSchema {
        try decode(run(["schema", "--provider", provider], input: nil))
    }

    // MARK: - Profile operations

    func list(provider: String) throws -> ListResponse {
        try decode(run(["list", "--provider", provider], input: nil))
    }

    func get(provider: String, _ name: String) throws -> ProfileResponse {
        try decode(run(["get", "--provider", provider, "--profile", name], input: nil))
    }

    func parse(provider: String, _ text: String) throws -> ParseResponse {
        try decode(run(["parse", "--provider", provider], input: text))
    }

    func delete(provider: String, _ name: String) throws {
        _ = try run(["delete", "--provider", provider, "--profile", name], input: nil)
    }

    func save(provider: String, _ name: String, fields: [String: String]) throws {
        let payload = try JSONEncoder().encode(SaveRequest(fields: fields))
        _ = try run(["save", "--provider", provider, "--profile", name], inputData: payload)
    }

    /// Raw text export (env / dotenv / ini / json). The caller owns secret
    /// hygiene: use the concealed pasteboard type or a user-chosen file.
    func export(provider: String, _ name: String, format: String) throws -> String {
        let data = try run(["export", "--provider", provider, "--profile", name, "--format", format], input: nil)
        return String(data: data, encoding: .utf8) ?? ""
    }

    func activate(provider: String, _ name: String) throws {
        _ = try run(["activate", "--provider", provider, "--profile", name], input: nil)
    }

    // MARK: - Workspaces

    func workspaces() throws -> [Workspace] {
        try decode(run(["ws", "list"], input: nil))
    }

    func saveWorkspace(_ workspace: Workspace) throws {
        let payload = try JSONEncoder().encode(workspace)
        _ = try run(["ws", "save"], inputData: payload)
    }

    func deleteWorkspace(_ name: String) throws {
        _ = try run(["ws", "delete", "--name", name], input: nil)
    }

    // MARK: - Process plumbing

    func decode<T: Decodable>(_ data: Data) throws -> T {
        try JSONDecoder().decode(T.self, from: data)
    }

    func run(_ args: [String], input: String?) throws -> Data {
        try run(args, inputData: input?.data(using: .utf8))
    }

    func run(_ args: [String], inputData: Data?) throws -> Data {
        let process = Process()
        let output = Pipe()
        let errorPipe = Pipe()
        let input = Pipe()
        process.executableURL = URL(fileURLWithPath: toolPath)
        process.arguments = args
        process.standardOutput = output
        process.standardError = errorPipe
        process.standardInput = input
        process.environment = Self.childEnvironment()

        try process.run()
        if let inputData {
            input.fileHandleForWriting.write(inputData)
        }
        try? input.fileHandleForWriting.close()

        // Drain stdout before waiting so a large payload cannot deadlock the
        // pipe; stderr is small (warnings / error text).
        let data = output.fileHandleForReading.readDataToEndOfFile()
        let errorData = errorPipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()

        if process.terminationStatus != 0 {
            let message = String(data: errorData, encoding: .utf8).flatMap { $0.isEmpty ? nil : $0 }
                ?? String(data: data, encoding: .utf8) ?? "Command failed"
            throw ServiceError.toolFailed(message.trimmingCharacters(in: .whitespacesAndNewlines))
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
    /// user is running the app) while pinning a safe, minimal PATH plus the
    /// Homebrew locations where the aws CLI is typically installed.
    static func childEnvironment() -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        return env
    }
}
