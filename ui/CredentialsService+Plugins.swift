import Foundation

private struct PluginUpdateRequest: Encodable {
    let changes: [String: Bool]
}

/// Plugin host registry + per-profile enable state (docs/PLATFORM.md phase
/// P1) — the `ezcloud plugins …` verbs.
extension CredentialsService {
    func listPlugins(profileID: String) throws -> [PluginDescriptor] {
        if let cached = cachedPlugins(profileID: profileID) { return cached }
        let descriptors: [PluginDescriptor] = try decode(run(["plugins", "list", "--profile", profileID], input: nil))
        storePluginSnapshot(descriptors, profileID: profileID)
        return descriptors
    }

    /// Applies every catalog toggle in one profile mutation. `changes` is a
    /// patch rather than a replacement list, so addon IDs unknown to this UI
    /// survive imports and future marketplace upgrades.
    @discardableResult
    func updatePlugins(profileID: String, changes: [String: Bool]) throws -> Profile {
        let payload = try JSONEncoder().encode(PluginUpdateRequest(changes: changes))
        let saved: Profile = try decode(run(["plugins", "update", "--profile", profileID], inputData: payload))
        storeProfile(saved)
        return saved
    }

    @discardableResult
    func enablePlugin(profileID: String, pluginID: String) throws -> Profile {
        let saved: Profile = try decode(run(["plugins", "enable", "--profile", profileID, "--id", pluginID], input: nil))
        storeProfile(saved)
        return saved
    }

    @discardableResult
    func disablePlugin(profileID: String, pluginID: String) throws -> Profile {
        let saved: Profile = try decode(run(["plugins", "disable", "--profile", profileID, "--id", pluginID], input: nil))
        storeProfile(saved)
        return saved
    }
}
