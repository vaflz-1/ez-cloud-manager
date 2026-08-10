import AppKit

/// Plugin list loading, grid data source, and opening each plugin in its own
/// window — the Hub's entire behavior (docs/PLATFORM.md phase P1).
extension ProfileWindowController {
    /// Tears down every window whose state is bound to this Hub's current
    /// profile. A rebind must create fresh controllers so no plugin can keep
    /// operating on the previously selected profile.
    func closeProfileBoundControllers() {
        let controllers: [NSWindowController?] = [
            cloudAccountsController,
            launchTemplatesController,
            transferController,
            catalogController
        ]
        for controller in controllers {
            guard let controller else { continue }
            if let childWindow = controller.window,
               let sheetParent = childWindow.sheetParent {
                sheetParent.endSheet(childWindow)
            }
            controller.close()
        }

        cloudAccountsController = nil
        launchTemplatesController = nil
        transferController = nil
        catalogController = nil
    }

    /// Keeps already-open addon sessions aligned with a mutation of this same
    /// profile. Disabled addons are closed immediately; sessions that can be
    /// rebound receive the fresh profile instead of retaining stale env vars.
    func reconcileProfileBoundControllers(environmentChanged: Bool) {
        let enabled = Set(profile.enabledPlugins)

        if environmentChanged || !enabled.contains(PluginID.cloudAccounts) {
            cloudAccountsController?.close()
            cloudAccountsController = nil
        }

        if enabled.contains(PluginID.launchTemplates), !environmentChanged {
            launchTemplatesController?.updateOwningProfile(profile)
        } else {
            launchTemplatesController?.close()
            launchTemplatesController = nil
        }

        if enabled.contains(PluginID.transfer) {
            transferController?.updateProfile(profile)
        } else {
            transferController?.close()
            transferController = nil
        }
    }

    func refreshHub() {
        let all = (try? service.listPlugins(profileID: profile.id)) ?? []
        enabledPlugins = all.filter { $0.enabled }
        emptyStateView.isHidden = !enabledPlugins.isEmpty
        gridScrollView.isHidden = enabledPlugins.isEmpty
        collectionView.reloadData()
    }

    // MARK: - NSCollectionViewDataSource

    func numberOfSections(in collectionView: NSCollectionView) -> Int { 1 }

    /// One item per enabled plugin, plus a permanent trailing "Add Plugins"
    /// tile (see AddPluginTileItem) — the grid is never JUST plugin cards
    /// once it's showing at all.
    func collectionView(_ collectionView: NSCollectionView, numberOfItemsInSection section: Int) -> Int {
        enabledPlugins.count + 1
    }

    func collectionView(_ collectionView: NSCollectionView, itemForRepresentedObjectAt indexPath: IndexPath) -> NSCollectionViewItem {
        if indexPath.item == enabledPlugins.count {
            return collectionView.makeItem(withIdentifier: AddPluginTileItem.reuseIdentifier, for: indexPath)
        }
        let item = collectionView.makeItem(withIdentifier: PluginCardItem.reuseIdentifier, for: indexPath)
        if let card = item as? PluginCardItem, indexPath.item < enabledPlugins.count {
            card.configure(enabledPlugins[indexPath.item])
        }
        return item
    }

    // MARK: - NSCollectionViewDelegate

    func collectionView(_ collectionView: NSCollectionView, didSelectItemsAt indexPaths: Set<IndexPath>) {
        defer { collectionView.deselectItems(at: indexPaths) }
        guard let idx = indexPaths.first?.item else { return }
        if idx == enabledPlugins.count {
            openCatalog()
            return
        }
        guard idx < enabledPlugins.count else { return }
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
        guard let win = window else { return }
        controller.present(on: win)
    }

    @objc func openManageProfiles() {
        NotificationCenter.default.post(name: .manageProfilesRequested, object: nil)
        // This is an action row inside the profile switcher's popup, not a
        // real profile selection — snap the popup's displayed title back to
        // this window's own profile rather than leaving it "stuck" showing
        // "Manage Profiles…".
        reloadProfileBar(selecting: profile.id)
    }

    /// The Hub's own Ko-fi toolbar button — a small, deliberate duplicate of
    /// AppDelegate's Help-menu action rather than new cross-controller
    /// plumbing (this codebase already duplicates one-liners like showError
    /// per-controller on purpose).
    @objc func openKoFi() {
        NSWorkspace.shared.open(AppDelegate.koFiURL)
    }
}
