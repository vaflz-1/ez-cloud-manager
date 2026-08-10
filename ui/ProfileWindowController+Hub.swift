import AppKit

/// Add-on list loading, grid data source and activation for one Workspace.
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

        if environmentChanged {
            cloudAccountsController?.close()
            cloudAccountsController = nil
        } else {
            cloudAccountsController?.updateOwningProfile(profile)
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
        do {
            enabledPlugins = try service.listPlugins(profileID: profile.id).filter { $0.enabled && !$0.isSystem }
            hubSnapshotProfileID = profile.id
            renderHub()
        } catch {
            showHubError(error.localizedDescription)
        }
    }

    func renderHub() {
        browseAddonsButton.title = "Browse Add-ons…"
        browseAddonsButton.target = self
        browseAddonsButton.action = #selector(openCatalog)
        workspaceMetaLabel.textColor = .secondaryLabelColor
        workspaceMetaLabel.toolTip = nil
        if enabledPlugins.isEmpty {
            configureEmptyStateForOnboarding()
        }
        updateWorkspaceHeader()
        browseAddonsButton.isHidden = enabledPlugins.isEmpty
        emptyStateView.isHidden = !enabledPlugins.isEmpty
        gridHostView.isHidden = enabledPlugins.isEmpty
        if !enabledPlugins.isEmpty { ensureGridBuilt() }
        collectionView?.reloadData()
    }

    @objc func retryHub() {
        refreshHub()
    }

    private func showHubError(_ message: String) {
        guard hubSnapshotProfileID == profile.id else {
            enabledPlugins = []
            renderHub()
            configureEmptyStateForError(message)
            browseAddonsButton.isHidden = true
            return
        }

        // A failed refresh must not erase the last good add-on snapshot. Keep
        // the current grid/empty state usable and turn the nearest action into
        // an explicit retry until a fresh snapshot arrives.
        renderHub()
        if enabledPlugins.isEmpty {
            configureEmptyStateForError(message)
        } else {
            workspaceMetaLabel.stringValue += "  ·  Refresh failed"
            workspaceMetaLabel.toolTip = message
            browseAddonsButton.title = "Try Again"
            browseAddonsButton.target = self
            browseAddonsButton.action = #selector(retryHub)
            browseAddonsButton.isHidden = false
        }
    }

    // MARK: - NSCollectionViewDataSource

    func numberOfSections(in collectionView: NSCollectionView) -> Int { 1 }

    /// One item per enabled add-on. Discovery is a stable header action, not
    /// a fake catalog card mixed into the user's installed tools.
    func collectionView(_ collectionView: NSCollectionView, numberOfItemsInSection section: Int) -> Int {
        enabledPlugins.count
    }

    func collectionView(_ collectionView: NSCollectionView, itemForRepresentedObjectAt indexPath: IndexPath) -> NSCollectionViewItem {
        let item = collectionView.makeItem(withIdentifier: PluginCardItem.reuseIdentifier, for: indexPath)
        if let card = item as? PluginCardItem, indexPath.item < enabledPlugins.count {
            let plugin = enabledPlugins[indexPath.item]
            card.configure(plugin)
            card.onPress = { [weak self] in self?.openPlugin(id: plugin.id) }
        }
        return item
    }

    // MARK: - NSCollectionViewDelegate

    func collectionView(_ collectionView: NSCollectionView, didSelectItemsAt indexPaths: Set<IndexPath>) {
        // Selection is navigation, not activation. Mouse-up and Return/Space
        // activate through HubCollectionView so keyboard arrows never launch
        // an add-on merely by moving focus.
    }

    func activateHubItem(at idx: Int) {
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

    @objc func openConnections() {
        let controller = cloudAccountsController ?? CloudAccountsWindowController(
            profile: profile,
            catalog: catalog,
            service: service
        )
        cloudAccountsController = controller
        controller.show()
    }

    @objc func openManageProfiles() {
        NotificationCenter.default.post(name: .manageProfilesRequested, object: nil)
        // This is an action row inside the profile switcher's popup, not a
        // real profile selection — snap the popup's displayed title back to
        // this window's own profile rather than leaving it "stuck" showing
        // "Manage Profiles…".
        reloadProfileBar(selecting: profile.id)
    }

}
