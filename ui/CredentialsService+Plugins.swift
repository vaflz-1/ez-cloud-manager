import Foundation

/// Plugin host registry + per-profile enable state (docs/PLATFORM.md phase
/// P1) — the `ezcloud plugins …` verbs.
extension CredentialsService {
    func listPlugins(profileID: String) throws -> [PluginDescriptor] {
        try decode(run(["plugins", "list", "--profile", profileID], input: nil))
    }

    @discardableResult
    func enablePlugin(profileID: String, pluginID: String) throws -> Profile {
        try decode(run(["plugins", "enable", "--profile", profileID, "--id", pluginID], input: nil))
    }

    @discardableResult
    func disablePlugin(profileID: String, pluginID: String) throws -> Profile {
        try decode(run(["plugins", "disable", "--profile", profileID, "--id", pluginID], input: nil))
    }
}
