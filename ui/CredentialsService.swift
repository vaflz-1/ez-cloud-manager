import Foundation

/// CredentialsService is the single boundary between the UI and the `ezcloud`
/// CLI. All subprocess spawning, stdin/stdout piping, and JSON decoding lives
/// here, so the rest of the app deals only in typed models.
///
/// This is also the natural seam for later work: moving storage to Keychain /
/// Secure Enclave, or adding a `--provider` argument, changes only this file.
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

    // MARK: - Typed operations

    func list() throws -> ListResponse {
        try decode(run(["list"], input: nil))
    }

    func get(_ name: String) throws -> ProfileResponse {
        try decode(run(["get", "--profile", name], input: nil))
    }

    func parse(_ text: String) throws -> ParseResponse {
        try decode(run(["parse"], input: text))
    }

    func delete(_ name: String) throws {
        _ = try run(["delete", "--profile", name], input: nil)
    }

    func save(_ name: String, fields: [String: String]) throws {
        let payload = try JSONEncoder().encode(SaveRequest(fields: fields))
        _ = try run(["save", "--profile", name], inputData: payload)
    }

    // MARK: - Process plumbing

    private func decode<T: Decodable>(_ data: Data) throws -> T {
        try JSONDecoder().decode(T.self, from: data)
    }

    private func run(_ args: [String], input: String?) throws -> Data {
        try run(args, inputData: input?.data(using: .utf8))
    }

    private func run(_ args: [String], inputData: Data?) throws -> Data {
        let process = Process()
        let output = Pipe()
        let input = Pipe()
        process.executableURL = URL(fileURLWithPath: toolPath)
        process.arguments = args
        process.standardOutput = output
        process.standardError = output
        process.standardInput = input
        process.environment = Self.childEnvironment()

        try process.run()
        if let inputData {
            input.fileHandleForWriting.write(inputData)
        }
        try? input.fileHandleForWriting.close()
        process.waitUntilExit()

        let data = output.fileHandleForReading.readDataToEndOfFile()
        if process.terminationStatus != 0 {
            let message = String(data: data, encoding: .utf8) ?? "Command failed"
            throw ServiceError.toolFailed(message)
        }
        return data
    }

    /// Environment for the child CLI. Inherits the caller's environment (so
    /// AWS_SHARED_CREDENTIALS_FILE and the real HOME keep working for whichever
    /// user is running the app) while pinning a safe, minimal PATH.
    private static func childEnvironment() -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin"
        return env
    }
}
