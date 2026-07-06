import AppKit

/// Workspaces: named groups of (provider, profile) references so one person
/// can keep clients/jobs/contexts apart. The popup scopes the sidebar; the
/// context menu manages membership. Metadata only — never credentials.
extension AppDelegate {
    func activeWorkspace() -> Workspace? {
        guard let name = activeWorkspaceName else { return nil }
        return workspaces.first { $0.name == name }
    }

    func reloadWorkspaces() {
        do {
            workspaces = try service.workspaces()
        } catch {
            workspaces = []
            setStatus("Workspaces unavailable: \(error.localizedDescription)")
        }
        if let active = activeWorkspaceName, !workspaces.contains(where: { $0.name == active }) {
            activeWorkspaceName = nil
        }
        rebuildWorkspacePopup()
    }

    func rebuildWorkspacePopup() {
        guard let popup = workspacePopup else { return }
        popup.removeAllItems()
        popup.addItem(withTitle: "All Profiles")
        popup.lastItem?.image = NSImage(systemSymbolName: "square.grid.2x2", accessibilityDescription: nil)
        for workspace in workspaces {
            popup.addItem(withTitle: "\(workspace.name)  (\(workspace.members.count))")
            popup.lastItem?.representedObject = workspace.name
            popup.lastItem?.image = NSImage(systemSymbolName: "briefcase.fill", accessibilityDescription: nil)
        }
        popup.menu?.addItem(.separator())
        let newItem = NSMenuItem(title: "New Workspace…", action: #selector(newWorkspace), keyEquivalent: "")
        newItem.target = self
        popup.menu?.addItem(newItem)
        let deleteItem = NSMenuItem(title: "Delete Current Workspace…", action: #selector(deleteCurrentWorkspace), keyEquivalent: "")
        deleteItem.target = self
        popup.menu?.addItem(deleteItem)

        if let active = activeWorkspaceName,
           let idx = workspaces.firstIndex(where: { $0.name == active }) {
            popup.selectItem(at: idx + 1) // +1 for "All Profiles"
        } else {
            popup.selectItem(at: 0)
        }
    }

    @objc func workspacePopupChanged(_ sender: NSPopUpButton) {
        activeWorkspaceName = sender.selectedItem?.representedObject as? String
        rebuildSidebarRows()
        let scope = activeWorkspaceName ?? "all profiles"
        setStatus("Showing \(scope)")
    }

    @objc func newWorkspace() {
        rebuildWorkspacePopup() // popup selection stays consistent after menu action

        let alert = NSAlert()
        alert.messageText = "New workspace"
        alert.informativeText = "A workspace groups profiles for one client/job/context. It stores references only — no credentials."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        field.placeholderString = "e.g. acme-prod"
        alert.accessoryView = field
        alert.addButton(withTitle: "Create")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        let name = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return }
        do {
            try service.saveWorkspace(Workspace(name: name, members: []))
            reloadWorkspaces()
            activeWorkspaceName = name
            rebuildWorkspacePopup()
            rebuildSidebarRows()
            setStatus("Workspace \(name) created — right-click profiles to add them")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func deleteCurrentWorkspace() {
        rebuildWorkspacePopup()
        guard let name = activeWorkspaceName else {
            showError("Select a workspace first (the popup is on “All Profiles”).")
            return
        }
        let alert = NSAlert()
        alert.messageText = "Delete workspace “\(name)”?"
        alert.informativeText = "Only the grouping is removed. No profiles or credentials are touched."
        alert.addButton(withTitle: "Delete Workspace")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do {
            try service.deleteWorkspace(name)
            activeWorkspaceName = nil
            reloadWorkspaces()
            rebuildSidebarRows()
            setStatus("Workspace \(name) deleted")
        } catch {
            showError(error.localizedDescription)
        }
    }

    // MARK: - Context menu (membership)

    /// Sidebar context menu; rebuilt on every open so it reflects the row
    /// under the pointer and current workspace membership.
    func buildProfileContextMenu() -> NSMenu {
        let menu = NSMenu()
        menu.delegate = self
        return menu
    }

    private func clickedProfile() -> (provider: String, name: String)? {
        let row = profilesTable.clickedRow
        guard row >= 0, row < sidebarRows.count, case .profile(let provider, let name) = sidebarRows[row] else {
            return nil
        }
        return (provider, name)
    }

    @objc func toggleWorkspaceMembership(_ sender: NSMenuItem) {
        guard let target = clickedProfile() ?? currentSelectionAsTarget(),
              let workspaceName = sender.representedObject as? String,
              var workspace = workspaces.first(where: { $0.name == workspaceName }) else { return }

        let member = WorkspaceMember(provider: target.provider, profile: target.name)
        if let idx = workspace.members.firstIndex(of: member) {
            workspace.members.remove(at: idx)
        } else {
            workspace.members.append(member)
        }
        do {
            try service.saveWorkspace(workspace)
            reloadWorkspaces()
            rebuildSidebarRows()
        } catch {
            showError(error.localizedDescription)
        }
    }

    private func currentSelectionAsTarget() -> (provider: String, name: String)? {
        guard let name = selectedProfileName else { return nil }
        return (selectedProvider, name)
    }
}

extension AppDelegate: NSMenuDelegate {
    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        guard let target = clickedProfileForMenu() else { return }

        let header = NSMenuItem(title: "\(target.name) — workspaces", action: nil, keyEquivalent: "")
        header.isEnabled = false
        menu.addItem(header)

        if workspaces.isEmpty {
            let hint = NSMenuItem(title: "No workspaces yet — create one from the sidebar popup", action: nil, keyEquivalent: "")
            hint.isEnabled = false
            menu.addItem(hint)
            return
        }
        for workspace in workspaces {
            let item = NSMenuItem(title: workspace.name, action: #selector(toggleWorkspaceMembership(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = workspace.name
            let isMember = workspace.members.contains(WorkspaceMember(provider: target.provider, profile: target.name))
            item.state = isMember ? .on : .off
            menu.addItem(item)
        }
    }

    private func clickedProfileForMenu() -> (provider: String, name: String)? {
        let row = profilesTable.clickedRow
        guard row >= 0, row < sidebarRows.count, case .profile(let provider, let name) = sidebarRows[row] else {
            return nil
        }
        return (provider, name)
    }
}
