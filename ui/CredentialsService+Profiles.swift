import Foundation

private struct ProfileCoreSaveRequest: Encodable {
    let id: String
    let name: String
    let envVars: [EnvVar]
    let expectedName: String
    let expectedEnvVars: [EnvVar]
}

/// Global profile management (docs/PLATFORM.md phase P0) — the
/// `ezcloud profile …` verbs. Unlike the credential-entry operations in
/// CredentialsService, these are provider-agnostic and app-global rather
/// than scoped to one window's extraEnv.
extension CredentialsService {
    /// Runs the v1.1 → v2.0 upgrade once (idempotent on the Go side via a
    /// marker file); safe to call on every launch.
    func ensureProfilesMigrated() throws {
        _ = try decode(run(["profile", "migrate"], input: nil)) as MigrateResponse
    }

    func listProfiles() throws -> [Profile] {
        try decode(run(["profile", "list"], input: nil))
    }

    func getProfile(id: String) throws -> Profile {
        try decode(run(["profile", "show", "--id", id], input: nil))
    }

    func createProfile(name: String) throws -> Profile {
        let created: Profile = try decode(run(["profile", "create", "--name", name], input: nil))
        postProfileListChange(.created, profileID: created.id)
        return created
    }

    func renameProfile(id: String, to newName: String) throws -> Profile {
        try decode(run(["profile", "rename", "--id", id, "--name", newName], input: nil))
    }

    func duplicateProfile(id: String, as newName: String? = nil) throws -> Profile {
        var args = ["profile", "duplicate", "--id", id]
        if let newName, !newName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            args += ["--name", newName]
        }
        let duplicate: Profile = try decode(run(args, input: nil))
        postProfileListChange(.duplicated, profileID: duplicate.id)
        return duplicate
    }

    func deleteProfile(id: String) throws {
        _ = try run(["profile", "delete", "--id", id], input: nil)
        postProfileListChange(.deleted, profileID: id)
    }

    /// Saves only the core fields the Profile Manager owns. Addon state stays
    /// server-owned, so an older editor snapshot cannot overwrite newer
    /// enabledPlugins/settings/windowState written by another addon window.
    /// The baseline core snapshot is a compare-and-swap guard: another
    /// process cannot silently replace newer name/env edits with this draft.
    func saveProfile(
        _ profile: Profile,
        expectedName: String,
        expectedEnvVars: [EnvVar]
    ) throws -> Profile {
        let request = ProfileCoreSaveRequest(
            id: profile.id,
            name: profile.name,
            envVars: profile.envVars,
            expectedName: expectedName,
            expectedEnvVars: expectedEnvVars
        )
        let payload = try JSONEncoder().encode(request)
        return try decode(run(["profile", "save"], inputData: payload))
    }

    /// Saves ONLY the Cloud Accounts plugin's settings blob (account scoping)
    /// on a profile, without touching any other field — the P1.5 replacement
    /// for editing Profile.accounts/showAllAccounts directly. Returns the
    /// whole updated profile, same convention as `plugins enable/disable`.
    @discardableResult
    func saveCloudAccountsSettings(profileID: String, _ settings: CloudAccountsSettings) throws -> Profile {
        let payload = try JSONEncoder().encode(settings)
        return try decode(run(["profile", "settings", "set", "--id", profileID, "--plugin", PluginID.cloudAccounts], inputData: payload))
    }

    /// Writes a `.ezprofile` zip to url. 0600: as sensitive as any other
    /// exported config (though profiles hold no secrets themselves).
    func exportProfile(id: String, to url: URL) throws {
        let data = try run(["profile", "export", "--id", id], input: nil)
        try data.write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    /// Imports a `.ezprofile` zip as a brand-new profile (fresh id; a name
    /// clash is resolved with a suffix on the Go side, never a failure).
    func importProfile(from url: URL) throws -> Profile {
        let imported: Profile = try decode(run(["profile", "import", "--input", url.path], input: nil))
        postProfileListChange(.imported, profileID: imported.id)
        return imported
    }

    private func postProfileListChange(_ mutation: ProfileListMutation, profileID: String) {
        NotificationCenter.default.post(
            name: .profileListDidChange,
            object: ProfileListChange(mutation, profileID: profileID)
        )
    }
}
