import Foundation

/// Registered provider backends and their field schemas — loaded once at
/// launch and shared read-only across every ProfileWindowController.
/// Providers/schemas are app-global (unlike a Profile's Accounts, Env Vars
/// or window state, which are per-window), so this is a plain class rather
/// than tied to any one window.
final class ProviderCatalog {
    /// Canonical provider ordering for the sidebar: the big three first,
    /// anything registered later sorts after them.
    static let providerOrder = ["aws", "gcp", "azure"]

    private(set) var providers: [ProviderInfo] = []
    private(set) var schemas: [String: ProviderSchema] = [:]

    /// Loads the provider list + field schemas once at startup. Failure
    /// leaves the catalog empty; callers fall back to the AWS legacy
    /// assumptions (secret-name heuristics) the same way the pre-multi-window
    /// app did.
    func load(using service: CredentialsService) throws {
        var infos = try service.providers()
        infos.sort { lhs, rhs in
            let li = Self.providerOrder.firstIndex(of: lhs.id) ?? Int.max
            let ri = Self.providerOrder.firstIndex(of: rhs.id) ?? Int.max
            return li == ri ? lhs.id < rhs.id : li < ri
        }
        providers = infos
        for info in infos {
            schemas[info.id] = try? service.schema(provider: info.id)
        }
    }

    func providerInfo(_ id: String) -> ProviderInfo? {
        providers.first { $0.id == id }
    }

    func providerDisplayName(_ id: String) -> String {
        providerInfo(id)?.displayName ?? id.uppercased()
    }

    func schema(_ id: String) -> ProviderSchema? {
        schemas[id]
    }
}
