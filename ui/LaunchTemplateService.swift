import Foundation

/// Launch Template operations, layered on the same `ezcloud` boundary.
/// These calls reach AWS through the user's aws CLI, so unlike profile
/// operations they are slow and must always be dispatched via `runAsync`.
extension CredentialsService {
    func launchTemplates(profile: String, region: String) throws -> [LaunchTemplate] {
        try decode(run(["lt", "templates", "--profile", profile, "--region", region], input: nil))
    }

    func launchTemplateVersions(profile: String, region: String, name: String) throws -> [LaunchTemplateVersion] {
        try decode(run(["lt", "versions", "--profile", profile, "--region", region, "--name", name], input: nil))
    }

    func launchTemplateData(profile: String, region: String, name: String, version: String) throws -> LaunchTemplateData {
        try decode(run(["lt", "get", "--profile", profile, "--region", region, "--name", name, "--version", version], input: nil))
    }

    /// Clone-edit-apply: creates a NEW version from `sourceVersion` with the
    /// edited fields and (optionally) makes it the default. The source
    /// version is never mutated — that is the safe rollback point.
    func applyLaunchTemplate(profile: String, region: String, name: String,
                             sourceVersion: String, description: String,
                             setDefault: Bool, fields: [String: String]) throws -> ApplyResponse {
        struct ApplyRequest: Codable {
            let sourceVersion: String
            let description: String
            let setDefault: Bool
            let fields: [String: String]
        }
        let payload = try JSONEncoder().encode(ApplyRequest(
            sourceVersion: sourceVersion, description: description,
            setDefault: setDefault, fields: fields))
        return try decode(run(["lt", "apply", "--profile", profile, "--region", region, "--name", name],
                              inputData: payload))
    }

    func setLaunchTemplateDefault(profile: String, region: String, name: String, version: String) throws {
        _ = try run(["lt", "set-default", "--profile", profile, "--region", region,
                     "--name", name, "--version", version], input: nil)
    }

    func deleteLaunchTemplateVersions(profile: String, region: String, name: String, versions: [String]) throws {
        _ = try run(["lt", "delete-versions", "--profile", profile, "--region", region,
                     "--name", name, "--versions", versions.joined(separator: ",")], input: nil)
    }
}
