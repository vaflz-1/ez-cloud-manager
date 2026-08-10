import AppKit
import Foundation

/// ProfileWindowController is the Workspace-bound **Add-on Hub**. It renders
/// enabled workflow packages as cards and opens each in its own window; trusted
/// Connections remain a permanent Platform surface in the toolbar.
/// Current compiled entrypoints are:
///   - CloudAccountsWindowController   — Platform Connections administration
///   - LaunchTemplatesWindowController — AWS-dependent Add-on
///   - TransferWindowController        — privileged Workspace transfer Add-on
///
/// Behavior is split across focused extensions:
///   - ProfileWindowController+Layout — window / toolbar / empty-state / grid building
///   - ProfileWindowController+Hub    — Add-on list loading, grid data source and activation
///
/// Terminology note: `self.profile: Profile` is the global container this
/// window is bound to (docs/PLATFORM.md's profile engine) — unrelated to the
/// pre-existing, unchanged concept of a per-provider credential entry (what
/// the CLI calls `--profile NAME`, now exclusively CloudAccountsWindowController's
/// concern). See internal/profile/profile.go's package doc for the same
/// distinction on the Go side.
final class ProfileWindowController: NSWindowController, NSWindowDelegate, NSToolbarDelegate,
    NSCollectionViewDataSource, NSCollectionViewDelegate {
    /// The global profile this window is bound to. Re-fetched when the
    /// Profile Manager saves a change to it — see handleProfileDidChange.
    var profile: Profile
    /// Shared, read-only provider/schema registry (loaded once by AppDelegate
    /// before any window opens) — passed through to CloudAccountsWindowController.
    let catalog: ProviderCatalog
    /// Boundary to the `ezcloud` CLI. Shared across windows — stateless
    /// aside from toolPath.
    let service: CredentialsService

    // One plugin window each, lazily created, reused/refocused — same
    // per-window-isolation idiom the pre-P1 code used for Launch Templates.
    var cloudAccountsController: CloudAccountsWindowController?
    var launchTemplatesController: LaunchTemplatesWindowController?
    var transferController: TransferWindowController?
    var catalogController: PluginCatalogWindowController?

    var collectionView: NSCollectionView?
    var emptyStateView: NSView!
    var emptyStateTitleLabel: NSTextField!
    var emptyStateSubtitleLabel: NSTextField!
    var emptyStateButton: NSButton!
    var gridHostView: NSView!
    var gridScrollView: NSScrollView?
    var workspaceTitleLabel: NSTextField!
    var workspaceMetaLabel: NSTextField!
    var browseAddonsButton: NSButton!
    /// This profile's enabled plugins, as rendered in the grid.
    var enabledPlugins: [PluginDescriptor] = []
    /// Bootstrap supplies the first menu and Hub payload in the same IPC.
    /// After that first paint, ordinary refresh paths remain authoritative.
    var preloadedProfiles: [Profile]?
    var hubSnapshotProfileID: String?
    private var hasRenderedPreloadedHub = false

    // MARK: Profile switcher toolbar item (ProfileWindowController+ProfileBar.swift)

    /// A leading toolbar item (not a separate view under the titlebar) —
    /// lists every profile, selection reflects THIS window's own profile.
    var profileBarPopup: NSPopUpButton!

    init(
        profile: Profile,
        catalog: ProviderCatalog,
        service: CredentialsService,
        preloadedProfiles: [Profile]? = nil,
        preloadedAddons: [PluginDescriptor]? = nil
    ) {
        self.profile = profile
        self.catalog = catalog
        self.service = service
        self.preloadedProfiles = preloadedProfiles
        self.enabledPlugins = preloadedAddons?.filter { $0.enabled && !$0.isSystem } ?? []
        self.hubSnapshotProfileID = preloadedAddons == nil ? nil : profile.id
        self.hasRenderedPreloadedHub = preloadedAddons != nil
        super.init(window: nil)
        buildWindow()
        window?.delegate = self
        configureToolbar()
        NotificationCenter.default.addObserver(self, selector: #selector(handleProfileDidChange(_:)),
                                                name: .profileDidChange, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(handleProfileListDidChange(_:)),
                                                name: .profileListDidChange, object: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    deinit {
        NotificationCenter.default.removeObserver(self)
    }

    /// Shows the window and (re)loads its plugin list — call once after
    /// construction, and again (via AppDelegate.openWindow) to refocus an
    /// already-open window.
    func show() {
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        if hasRenderedPreloadedHub {
            hasRenderedPreloadedHub = false
            renderHub()
        } else {
            refreshHub()
        }
    }

    func windowWillClose(_ notification: Notification) {
        closeProfileBoundControllers()
        NotificationCenter.default.post(name: .profileWindowWillClose, object: self)
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        authorizeProfileContextExit()
    }

    /// Programmatic lifecycle paths do not call NSWindowDelegate's
    /// windowShouldClose. Every rebind/delete/external-context transition must
    /// explicitly pass this same draft guard before closing Connections.
    func authorizeProfileContextExit() -> Bool {
        cloudAccountsController?.confirmDiscardConnectionChanges() ?? true
    }

    func canApplyRefreshedSnapshot(_ updated: Profile?) -> Bool {
        guard updated == nil || updated?.envVars != profile.envVars else { return true }
        return cloudAccountsController?.authorizeDiscardConnectionChanges() ?? true
    }

    /// The Profile Manager saved a change to this window's profile (name,
    /// Accounts, env vars, enabled plugins, …) — re-fetch and re-render
    /// rather than going stale until the next manual refresh.
    @objc private func handleProfileDidChange(_ note: Notification) {
        guard let changedID = note.object as? String else { return }
        // Any profile's change (not just this window's own) can rename an
        // entry the profile switcher's popup lists — keep it in sync either way.
        reloadProfileBar(selecting: profile.id)
        guard changedID == profile.id else { return }
        guard let updated = try? service.getProfile(id: profile.id) else { return }
        let environmentChanged = profile.envVars != updated.envVars
        // The normal Profile Manager path asks before persisting an env
        // change. Keep this defensive gate for any future caller that posts
        // the notification directly: a live Connections draft remains bound
        // to its original cloud context until the user chooses to discard it.
        guard !environmentChanged || authorizeProfileContextExit() else { return }
        profile = updated
        window?.title = Product.workspaceTitle(updated.name)
        updateWorkspaceHeader()
        refreshHub()
        reconcileProfileBoundControllers(environmentChanged: environmentChanged)
    }

    /// Create/duplicate/import/delete all change the rows offered by every
    /// open Hub's profile switcher. The deleted profile's own Hub is closed
    /// by AppDelegate, so it does not need one last popup rebuild.
    @objc private func handleProfileListDidChange(_ note: Notification) {
        guard let change = note.object as? ProfileListChange else { return }
        guard change.mutation != .deleted || change.profileID != profile.id else { return }
        reloadProfileBar(selecting: profile.id)
    }

    /// Called only by AppDelegate after this profile has been deleted. Child
    /// addon windows and sheets are closed first; `windowWillClose` repeats
    /// the cleanup safely because `closeProfileBoundControllers` is
    /// idempotent once its controller references have been cleared.
    func closeAfterProfileDeletion() {
        closeProfileBoundControllers()
        close()
    }

    /// Rebinds THIS window (same NSWindow, same identity) to a different
    /// profile — the Hub profile switcher's normal path when the target has
    /// no window of its own already open (see AppDelegate's
    /// `.profileSwitchRequested` handler, which chooses refocus-vs-rebind).
    @discardableResult
    func rebind(to newProfile: Profile) -> Bool {
        guard authorizeProfileContextExit() else { return false }
        closeProfileBoundControllers()
        enabledPlugins = []
        hubSnapshotProfileID = nil
        profile = newProfile
        window?.title = Product.workspaceTitle(newProfile.name)
        updateWorkspaceHeader()
        refreshHub()
        reloadProfileBar(selecting: newProfile.id)
        return true
    }

    /// Snaps the profile switcher's popup back to THIS window's own profile
    /// — used when the user picked a profile that turned out to already have
    /// its own open window (AppDelegate refocuses that one instead of
    /// rebinding this one), so the popup never shows a profile this window
    /// isn't actually bound to.
    func revertProfilePopupSelection() {
        reloadProfileBar(selecting: profile.id)
    }

    /// Applies one explicit app-wide disk refresh without launching another
    /// subprocess per open window. CredentialsService has already replaced
    /// its immutable snapshot before AppDelegate calls this method.
    func applyRefreshedSnapshot(_ updated: Profile, allProfiles: [Profile]) {
        let environmentChanged = profile.envVars != updated.envVars
        profile = updated
        preloadedProfiles = allProfiles
        window?.title = Product.workspaceTitle(updated.name)
        updateWorkspaceHeader()
        reloadProfileBar(selecting: updated.id)
        refreshHub()
        reconcileProfileBoundControllers(environmentChanged: environmentChanged)
    }
}

/// Cross-window notifications tying the Profile Manager, individual profile
/// windows and AppDelegate together without direct references between them.
extension Notification.Name {
    /// Posted after any targeted profile mutation; object: the changed
    /// profile's id (String).
    static let profileDidChange = Notification.Name("EZCloudManager.profileDidChange")
    /// Posted by ProfileWindowController.windowWillClose; object: the
    /// controller itself, so AppDelegate can drop it from its registry.
    static let profileWindowWillClose = Notification.Name("EZCloudManager.profileWindowWillClose")
    /// Posted by the Hub's profile switcher menu's "Manage Profiles…" row;
    /// AppDelegate opens the (app-global, singleton) Profile Manager window
    /// in response — the same action the old File menu item triggered
    /// directly.
    static let manageProfilesRequested = Notification.Name("EZCloudManager.manageProfilesRequested")
    /// Posted by the Hub's profile switcher (ProfileWindowController+ProfileBar.swift)
    /// when the user picks a different profile; object: a ProfileSwitchRequest.
    /// AppDelegate decides whether to rebind the requesting window in place
    /// or refocus an already-open window for the target profile (the
    /// one-window-per-profile invariant must never be broken).
    static let profileSwitchRequested = Notification.Name("EZCloudManager.profileSwitchRequested")
    /// Posted by Workspaces on show, and available from File → Refresh, so
    /// mutations made through the headless CLI become visible without an app
    /// restart. AppDelegate coalesces it into one fresh profile-list process.
    static let refreshWorkspaceStateRequested = Notification.Name("Kervik.refreshWorkspaceStateRequested")
    /// Synchronous preflight before an in-app workspace delete reaches disk.
    static let profileDeletionPreflightRequested = Notification.Name("Kervik.profileDeletionPreflightRequested")
    /// Synchronous preflight before an in-app environment change reaches
    /// disk. Addon sessions must never be moved across cloud contexts with a
    /// local draft still attached.
    static let profileEnvironmentChangePreflightRequested = Notification.Name("Kervik.profileEnvironmentChangePreflightRequested")
}

final class ProfileContextChangePreflight {
    let profileID: String
    var allowed = true

    init(profileID: String) {
        self.profileID = profileID
    }
}
