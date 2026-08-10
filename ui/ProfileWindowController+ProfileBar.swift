import AppKit

/// Payload for `.profileSwitchRequested` — a pure-Swift object (not @objc)
/// delivered via the block-based `addObserver(forName:object:queue:using:)`
/// API AppDelegate already uses, not a target/action selector.
final class ProfileSwitchRequest {
    let from: ProfileWindowController
    let targetProfileID: String
    init(from: ProfileWindowController, targetProfileID: String) {
        self.from = from
        self.targetProfileID = targetProfileID
    }
}

/// The Hub's profile switcher — a LEADING TOOLBAR ITEM (an NSPopUpButton as
/// the item's view, NOT a separate bar view under the titlebar), always
/// showing which profile this window is bound to. Picking a different
/// profile REBINDS this window in place (same NSWindow, new profile/env/
/// plugins/title) unless that profile already has its own open window, in
/// which case AppDelegate refocuses that window instead and this popup snaps
/// back — the one-window-per-profile invariant (AppDelegate.windowControllers
/// keyed by id) must never be broken by the switcher. "Manage Profiles" has
/// no toolbar item of its own any more — it's a row in this menu.
extension ProfileWindowController {
    private static let dotDiameter: CGFloat = 8

    func buildProfileSwitcherToolbarItem(identifier: NSToolbarItem.Identifier) -> NSToolbarItem {
        let item = NSToolbarItem(itemIdentifier: identifier)
        item.label = "Workspace"
        item.toolTip = "Switch, create, or manage workspaces"

        let popup = NSPopUpButton()
        popup.pullsDown = false
        popup.translatesAutoresizingMaskIntoConstraints = false
        profileBarPopup = popup
        reloadProfileBar(selecting: profile.id)

        item.view = popup
        return item
    }

    /// Rebuilds the popup's menu — one row per profile (leading dot: that
    /// profile's single-provider color if its scope is one provider and not
    /// "show all", else neutral; current profile checked), a separator, then
    /// "New Profile…" and "Manage Profiles…" — and selects `selectedID`'s
    /// row. Called at build time and again whenever any profile changes
    /// (rename, scope edit, …) so the switcher never goes stale. Because
    /// NSPopUpButton (non-pull-down) always displays whichever item was last
    /// selected, this is also how the action rows below get "un-stuck" after
    /// being clicked.
    func reloadProfileBar(selecting selectedID: String) {
        guard let popup = profileBarPopup else { return }
        let all = preloadedProfiles ?? (try? service.listProfiles()) ?? []
        preloadedProfiles = nil
        let menu = NSMenu()
        for p in all {
            let item = NSMenuItem(title: p.name, action: #selector(profileRowSelected(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = p.id
            // The host deliberately does not decode any plugin's settings to
            // decorate this core control. A future contribution point can let
            // an enabled plugin supply profile adornments explicitly.
            item.image = ProviderStyle.neutralDot(diameter: Self.dotDiameter)
            item.state = (p.id == selectedID) ? .on : .off
            menu.addItem(item)
        }
        menu.addItem(.separator())
        let newItem = NSMenuItem(title: "New Workspace…", action: #selector(createProfileFromSwitcher), keyEquivalent: "")
        newItem.target = self
        menu.addItem(newItem)
        let manageItem = NSMenuItem(title: "Manage Workspaces…", action: #selector(openManageProfiles), keyEquivalent: "")
        manageItem.target = self
        menu.addItem(manageItem)

        popup.menu = menu
        if let idx = all.firstIndex(where: { $0.id == selectedID }) {
            popup.selectItem(at: idx)
        }
    }

    /// A profile row was picked directly (not "New Profile…"/"Manage
    /// Profiles…") — request a switch; AppDelegate decides refocus vs
    /// rebind (see `.profileSwitchRequested`'s doc comment on Notification.Name).
    @objc private func profileRowSelected(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String, id != profile.id else { return }
        NotificationCenter.default.post(name: .profileSwitchRequested,
                                         object: ProfileSwitchRequest(from: self, targetProfileID: id))
    }

    /// The same "New profile" prompt Manage Profiles' own New… button uses
    /// (ProfileCreationPrompt, in ProfileManagerWindowController.swift). The
    /// created profile is requested the same way a popup row would be,
    /// naturally taking the "no existing window" branch and rebinding.
    @objc func createProfileFromSwitcher() {
        guard let name = ProfileCreationPrompt.run() else {
            reloadProfileBar(selecting: profile.id) // snap back from the stuck "New Profile…" row
            return
        }
        do {
            let created = try service.createProfile(name: name)
            NotificationCenter.default.post(name: .profileSwitchRequested,
                                             object: ProfileSwitchRequest(from: self, targetProfileID: created.id))
        } catch {
            let alert = NSAlert()
            alert.messageText = "New Workspace"
            alert.informativeText = error.localizedDescription
            alert.alertStyle = .warning
            alert.runModal()
            reloadProfileBar(selecting: profile.id)
        }
    }
}
