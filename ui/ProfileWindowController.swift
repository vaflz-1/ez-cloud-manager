import AppKit
import Foundation

/// ProfileWindowController is the **Plugin Hub** — the window one app window
/// binds to (docs/PLATFORM.md phase P0's one-window-one-profile model is
/// unchanged; only its CONTENT changes here, P1). It shows this profile's
/// enabled plugins as cards in a grid (or an empty-skeleton state if none are
/// enabled yet) and opens each plugin in its own separate window — it never
/// hosts plugin UI directly. Everything the pre-P1 credentials browser did
/// now lives in CloudAccountsWindowController, one of three built-in plugins
/// this profile can enable (see internal/plugin.Builtins):
///   - CloudAccountsWindowController  — the credentials browser (moved verbatim)
///   - LaunchTemplatesWindowController — EC2 Launch Templates (unchanged class, new entry point)
///   - TransferWindowController        — whole-profile .ezprofile export/import (new)
///
/// Behavior is split across focused extensions:
///   - ProfileWindowController+Layout — window / toolbar / empty-state / grid building
///   - ProfileWindowController+Hub    — plugin list loading, grid data source, opening plugins
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

    var collectionView: NSCollectionView!
    var emptyStateView: NSView!
    var gridScrollView: NSScrollView!
    /// This profile's enabled plugins, as rendered in the grid.
    var enabledPlugins: [PluginDescriptor] = []

    init(profile: Profile, catalog: ProviderCatalog, service: CredentialsService) {
        self.profile = profile
        self.catalog = catalog
        self.service = service
        super.init(window: nil)
        buildWindow()
        window?.delegate = self
        configureToolbar()
        NotificationCenter.default.addObserver(self, selector: #selector(handleProfileDidChange(_:)),
                                                name: .profileDidChange, object: nil)
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
        refreshHub()
    }

    func windowWillClose(_ notification: Notification) {
        NotificationCenter.default.post(name: .profileWindowWillClose, object: self)
    }

    /// The Profile Manager saved a change to this window's profile (name,
    /// Accounts, env vars, enabled plugins, …) — re-fetch and re-render
    /// rather than going stale until the next manual refresh.
    @objc private func handleProfileDidChange(_ note: Notification) {
        guard let changedID = note.object as? String, changedID == profile.id else { return }
        guard let updated = try? service.getProfile(id: profile.id) else { return }
        profile = updated
        window?.title = "EZ Cloud Manager — \(updated.name)"
        refreshHub()
    }
}

/// Cross-window notifications tying the Profile Manager, individual profile
/// windows and AppDelegate together without direct references between them.
extension Notification.Name {
    /// Posted by ProfileManagerWindowController after a whole-object save;
    /// object: the changed profile's id (String).
    static let profileDidChange = Notification.Name("EZCloudManager.profileDidChange")
    /// Posted by ProfileWindowController.windowWillClose; object: the
    /// controller itself, so AppDelegate can drop it from its registry.
    static let profileWindowWillClose = Notification.Name("EZCloudManager.profileWindowWillClose")
    /// Posted by the Hub's "Manage Profiles" toolbar item; AppDelegate opens
    /// the (app-global, singleton) Profile Manager window in response — the
    /// same action the old File menu item triggered directly.
    static let manageProfilesRequested = Notification.Name("EZCloudManager.manageProfilesRequested")
}
