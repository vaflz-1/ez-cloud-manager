import AppKit

/// Plugin list loading, grid data source, and opening each plugin in its own
/// window — the Hub's entire behavior (docs/PLATFORM.md phase P1).
extension ProfileWindowController {
    func refreshHub() {
        let all = (try? service.listPlugins(profileID: profile.id)) ?? []
        enabledPlugins = all.filter { $0.enabled }
        emptyStateView.isHidden = !enabledPlugins.isEmpty
        gridScrollView.isHidden = enabledPlugins.isEmpty
        collectionView.reloadData()
    }

    // MARK: - NSCollectionViewDataSource

    func numberOfSections(in collectionView: NSCollectionView) -> Int { 1 }

    func collectionView(_ collectionView: NSCollectionView, numberOfItemsInSection section: Int) -> Int {
        enabledPlugins.count
    }

    func collectionView(_ collectionView: NSCollectionView, itemForRepresentedObjectAt indexPath: IndexPath) -> NSCollectionViewItem {
        let item = collectionView.makeItem(withIdentifier: PluginCardItem.reuseIdentifier, for: indexPath)
        if let card = item as? PluginCardItem, indexPath.item < enabledPlugins.count {
            card.configure(enabledPlugins[indexPath.item])
        }
        return item
    }

    // MARK: - NSCollectionViewDelegate

    func collectionView(_ collectionView: NSCollectionView, didSelectItemsAt indexPaths: Set<IndexPath>) {
        defer { collectionView.deselectItems(at: indexPaths) }
        guard let idx = indexPaths.first?.item, idx < enabledPlugins.count else { return }
        openPlugin(id: enabledPlugins[idx].id)
    }

    // MARK: - Opening plugins

    /// Opens (or refocuses) the window for one enabled plugin. Each plugin
    /// window is bound to THIS window's profile — the same per-window
    /// isolation P0 established for Launch Templates, now generalized.
    func openPlugin(id: String) {
        switch id {
        case PluginID.cloudAccounts:
            let controller = cloudAccountsController ?? CloudAccountsWindowController(profile: profile, catalog: catalog, service: service)
            cloudAccountsController = controller
            controller.show()
        case PluginID.launchTemplates:
            let controller = launchTemplatesController ?? LaunchTemplatesWindowController(service: service)
            launchTemplatesController = controller
            controller.present(owningProfile: profile)
        case PluginID.transfer:
            let controller = transferController ?? TransferWindowController(profile: profile, service: service)
            transferController = controller
            controller.show()
        default:
            break // unknown id (future marketplace plugin) — nothing to open yet
        }
    }

    @objc func openCatalog() {
        let controller = catalogController ?? PluginCatalogWindowController(service: service, profileID: profile.id)
        catalogController = controller
        controller.onChange = { [weak self] in self?.refreshHub() }
        guard let win = window else { return }
        controller.present(on: win)
    }

    @objc func openManageProfiles() {
        NotificationCenter.default.post(name: .manageProfilesRequested, object: nil)
    }

    /// The Hub's own Ko-fi toolbar button — a small, deliberate duplicate of
    /// AppDelegate's Help-menu action rather than new cross-controller
    /// plumbing (this codebase already duplicates one-liners like showError
    /// per-controller on purpose).
    @objc func openKoFi() {
        NSWorkspace.shared.open(AppDelegate.koFiURL)
    }
}
