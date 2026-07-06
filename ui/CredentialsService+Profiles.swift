import Foundation

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
        try decode(run(["profile", "create", "--name", name], input: nil))
    }

    func renameProfile(id: String, to newName: String) throws -> Profile {
        try decode(run(["profile", "rename", "--id", id, "--name", newName], input: nil))
    }

    func duplicateProfile(id: String, as newName: String? = nil) throws -> Profile {
        var args = ["profile", "duplicate", "--id", id]
        if let newName, !newName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            args += ["--name", newName]
        }
        return try decode(run(args, input: nil))
    }

    func deleteProfile(id: String) throws {
        _ = try run(["profile", "delete", "--id", id], input: nil)
    }

    /// Whole-object PUT — same convention as the old `saveWorkspace`. Sends
    /// every field back, including any Accounts/EnvVars/Settings edits made
    /// in the Profile Manager, and returns the saved (server-normalized)
    /// profile.
    func saveProfile(_ profile: Profile) throws -> Profile {
        let payload = try JSONEncoder().encode(profile)
        return try decode(run(["profile", "save"], inputData: payload))
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
        try decode(run(["profile", "import", "--input", url.path], input: nil))
    }
}
