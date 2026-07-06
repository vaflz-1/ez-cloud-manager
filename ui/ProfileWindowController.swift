import AppKit
import Foundation

/// ProfileWindowController owns one window bound to exactly one global
/// Profile (docs/PLATFORM.md phase P0) — two windows on two profiles are
/// fully independent contexts: different Accounts filtering, different env
/// vars threaded into every `ezcloud` call, their own Launch Templates
/// session. Its behavior is split across focused extensions, mirroring the
/// pre-multi-window AppDelegate's own module split:
///   - ProfileWindowController+Layout     — window / sidebar / detail view building
///   - ProfileWindowController+Tables     — NSTableView data source & delegate
///   - ProfileWindowController+Actions    — user-triggered actions (add/save/delete/parse)
///   - ProfileWindowController+State      — credential-entry loading, field rows, status helpers
///   - ProfileWindowController+Transfer   — export / import / compare / activate
///
/// Terminology note: this file (and its extensions) uses "profile" in two
/// senses. `self.profile: Profile` is the new global container this window
/// is bound to (docs/PLATFORM.md's profile engine). `profilesByProvider`,
/// `loadProfile(provider:name:)`, `selectedProfileName` and similar are the
/// pre-existing, UNCHANGED concept of a per-provider credential entry (what
/// the CLI calls `--profile NAME`). See internal/profile/profile.go's
/// package doc for the same distinction on the Go side.
///
/// Members are `internal` (not `private`) because they are shared across
/// those extension files; the app is a single-module binary, so nothing
/// leaks.
final class ProfileWindowController: NSWindowController, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate, NSSearchFieldDelegate, NSTextViewDelegate, NSToolbarDelegate {
    /// The global profile this window is bound to. Re-fetched when the
    /// Profile Manager saves a change to it — see handleProfileDidChange.
    var profile: Profile
    /// Shared, read-only provider/schema registry (loaded once by AppDelegate
    /// before any window opens).
    let catalog: ProviderCatalog
    /// Boundary to the `ezcloud` CLI. Shared across windows — stateless
    /// aside from toolPath.
    let service: CredentialsService

    var addRemoveControl: NSSegmentedControl!
    var profilesTable: NSTableView!
    var fieldsTable: NSTableView!
    var profileNameField: NSTextField!
    var profileModeLabel: NSTextField!
    var providerPopup: NSPopUpButton!
    var pasteView: NSTextView!
    var pasteLabel: NSTextField!
    var statusLabel: NSTextField!
    var variablesTitleLabel: NSTextField!
    var variablesSummaryLabel: NSTextField!
    var saveButton: NSButton!
    var activateButton: NSButton!
    var exportButton: NSPopUpButton!
    var profileSearchField: NSSearchField!

    // MARK: Multi-provider state (credential entries — see the terminology note above)

    /// Full profile lists per provider; the sidebar renders a filtered view.
    var profilesByProvider: [String: [ProfileSummary]] = [:]
    /// Storage path per provider (shown in status / delete confirmations).
    var pathsByProvider: [String: String] = [:]
    /// The rendered sidebar (headers + profiles after the search + Accounts filter).
    var sidebarRows: [SidebarRow] = []
    /// True while rebuildSidebarRows() reloads the table — reloadData fires
    /// spurious selection-change notifications that must not clear the detail.
    var isRebuildingSidebar = false
    /// Profile whose selection was hidden by the current filter; restored
    /// automatically the moment the filter lets it back in.
    var hiddenSelection: (provider: String, name: String)?
    /// 3px accent stripe on the PROFILE card, tinted by the editing provider.
    var profileCardStripe: NSView!

    /// Provider owning the currently selected/edited credential entry.
    var selectedProvider = "aws"
    var selectedProfileName: String?
    var fieldRows: [FieldRow] = []
    var lastAutoParsedPaste = ""

    /// Retains this window's own Launch Templates window — one per profile
    /// window, so two windows never fight over one AWS-account-bound session.
    var launchTemplatesController: LaunchTemplatesWindowController?

    /// Presentation layer over `fieldRows`: section headers + field references,
    /// so the flat model drives grouped, collapsible rows (Common/Advanced/…).
    var displayItems: [VarItem] = []
    var collapsedSections: Set<VarSection> = []

    init(profile: Profile, catalog: ProviderCatalog, service: CredentialsService) {
        self.profile = profile
        self.catalog = catalog
        self.service = service
        super.init(window: nil)
        buildWindow()
        window?.delegate = self
        configureToolbar()
        rebuildProviderPopup()
        NotificationCenter.default.addObserver(self, selector: #selector(handleProfileDidChange(_:)),
                                                name: .profileDidChange, object: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    deinit {
        NotificationCenter.default.removeObserver(self)
    }

    /// Shows the window and (re)loads its credential-entry data — call once
    /// after construction, and again (via AppDelegate.openWindow) to refocus
    /// an already-open window.
    func show() {
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        refreshProfiles()
    }

    func windowWillClose(_ notification: Notification) {
        NotificationCenter.default.post(name: .profileWindowWillClose, object: self)
    }

    /// The Profile Manager saved a change to this window's profile (name,
    /// Accounts, env vars, …) — re-fetch and re-render rather than going
    /// stale until the next manual refresh.
    @objc private func handleProfileDidChange(_ note: Notification) {
        guard let changedID = note.object as? String, changedID == profile.id else { return }
        guard let updated = try? service.getProfile(id: profile.id) else { return }
        profile = updated
        window?.title = "EZ Cloud Manager — \(updated.name)"
        rebuildSidebarRows()
        setStatus("Profile settings updated")
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
}

/// Logical grouping of credential fields in the Variables editor.
enum VarSection: String, CaseIterable {
    case common = "Common"
    case advanced = "Advanced"
    case additional = "Additional"
}

/// One row in the Variables table: a section header or a reference to a `fieldRows` index.
enum VarItem {
    case header(VarSection)
    case field(Int)
}

/// One row in the profiles sidebar: a provider group header, a profile, a
/// "TOOLS" subheader, or one of the provider's tools/services.
enum SidebarRow {
    case header(provider: String, title: String, count: Int)
    case profile(provider: String, name: String)
    case subheader(String)
    case tool(provider: String, id: String, title: String, symbol: String)
}
