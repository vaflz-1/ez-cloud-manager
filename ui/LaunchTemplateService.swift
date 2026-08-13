import Foundation

/// Launch Template operations, layered on the same `ezcloud` boundary.
/// These calls reach AWS through the user's aws CLI, so unlike profile
/// operations they are slow and must always be dispatched via `runAsync`.
///
/// Every call takes an `extraEnv` the caller resolves from the owning
/// window's Profile (`owningProfile.envVars.asDictionary()`) — same
/// convention as CredentialsService's own profile operations. P0 never
/// threaded a profile's env vars into these calls at all; P1 fixes that
/// while it's already touching every call site for the AWS-profile picker
/// (docs/PLATFORM.md phase P1).
extension CredentialsService {
    func launchTemplates(workspaceID: String, profile: String, region: String, extraEnv: [String: String] = [:]) throws -> [LaunchTemplate] {
        try decode(run(["lt", "templates", "--workspace", workspaceID, "--profile", profile, "--region", region], input: nil, extraEnv: extraEnv))
    }

    func launchTemplateVersions(workspaceID: String, profile: String, region: String, name: String, extraEnv: [String: String] = [:]) throws -> [LaunchTemplateVersion] {
        try decode(run(["lt", "versions", "--workspace", workspaceID, "--profile", profile, "--region", region, "--name", name], input: nil, extraEnv: extraEnv))
    }

    func launchTemplateData(workspaceID: String, profile: String, region: String, name: String, version: String, extraEnv: [String: String] = [:]) throws -> LaunchTemplateData {
        try decode(run(["lt", "get", "--workspace", workspaceID, "--profile", profile, "--region", region, "--name", name, "--version", version], input: nil, extraEnv: extraEnv))
    }

    /// Clone-edit-apply: creates a NEW version from `sourceVersion` with the
    /// edited fields and (optionally) makes it the default. The source
    /// version is never mutated — that is the safe rollback point.
    func applyLaunchTemplate(workspaceID: String, profile: String, region: String, name: String,
                             sourceVersion: String, description: String,
                             setDefault: Bool, fields: [String: String], extraEnv: [String: String] = [:]) throws -> ApplyResponse {
        struct ApplyRequest: Codable {
            let sourceVersion: String
            let description: String
            let setDefault: Bool
            let fields: [String: String]
        }
        let payload = try JSONEncoder().encode(ApplyRequest(
            sourceVersion: sourceVersion, description: description,
            setDefault: setDefault, fields: fields))
        return try decode(run(["lt", "apply", "--workspace", workspaceID, "--profile", profile, "--region", region, "--name", name],
                              inputData: payload, extraEnv: extraEnv))
    }

    func setLaunchTemplateDefault(workspaceID: String, profile: String, region: String, name: String, version: String, extraEnv: [String: String] = [:]) throws {
        _ = try run(["lt", "set-default", "--workspace", workspaceID, "--profile", profile, "--region", region,
                     "--name", name, "--version", version], input: nil, extraEnv: extraEnv)
    }

    func deleteLaunchTemplateVersions(workspaceID: String, profile: String, region: String, name: String, versions: [String], extraEnv: [String: String] = [:]) throws {
        _ = try run(["lt", "delete-versions", "--workspace", workspaceID, "--profile", profile, "--region", region,
                     "--name", name, "--versions", versions.joined(separator: ",")], input: nil, extraEnv: extraEnv)
    }
}
