import AppKit
import Foundation

/// CloudAccountsWindowController is the **Cloud Accounts** built-in plugin
/// (internal/plugin.CloudAccountsID) — the credentials browser that WAS
/// ProfileWindowController's entire window before the P1 plugin host
/// (docs/PLATFORM.md phase P1) split it out. This is a pure extraction: the
/// sidebar + detail editor + save/delete/parse/export/import/compare/activate
/// behavior is unchanged; only its home (its own window, opened from a Hub
/// plugin card instead of always-on) and its class name moved.
///
/// One instance per Hub window, lazily created and reused/refocused — two
/// profile windows must never fight over one edit surface, exactly like P0's
/// per-window isolation for Launch Templates.
///
/// Behavior is split across focused extensions, mirroring the pre-P1 split:
///   - CloudAccountsWindowController+Layout   — window / sidebar / detail view building
///   - CloudAccountsWindowController+Tables   — NSTableView data source & delegate
///   - CloudAccountsWindowController+Actions  — user-triggered actions (add/save/delete/parse)
///   - CloudAccountsWindowController+State    — credential-entry loading, field rows, status helpers
///   - CloudAccountsWindowController+Transfer — export / import / compare / activate
///
/// Terminology note: this file (and its extensions) uses "profile" in two
/// senses. `self.profile: Profile` is the global container this window is
/// bound to (docs/PLATFORM.md's profile engine). `profilesByProvider`,
/// `loadProfile(provider:name:)`, `selectedProfileName` and similar are the
/// pre-existing, UNCHANGED concept of a per-provider credential entry (what
/// the CLI calls `--profile NAME`). See internal/profile/profile.go's
/// package doc for the same distinction on the Go side.
///
/// Members are `internal` (not `private`) because they are shared across
/// those extension files; the app is a single-module binary, so nothing
/// leaks.
final class CloudAccountsWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate, NSSearchFieldDelegate, NSTextViewDelegate, NSToolbarDelegate {
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
    /// Faint overlay hint shown only while pasteView is empty — an empty
    /// NSTextView otherwise reads as a void, not an input.
    var pasteViewPlaceholder: NSTextField!
    /// Faint centered hint shown only when fieldRows has no populated
    /// values — an empty grid otherwise looks broken rather than "add one".
    var fieldsEmptyHintLabel: NSTextField!
    var statusLabel: NSTextField!
    var variablesTitleLabel: NSTextField!
    var variablesSummaryLabel: NSTextField!
    var saveButton: NSButton!
    var activateButton: NSButton!
    var exportButton: NSPopUpButton!
    var profileSearchField: NSSearchField!

    // MARK: Test Connection (CloudAccountsWindowController+Diagnostics.swift)

    var testConnectionButton: NSButton!
    var testConnectionSpinner: NSProgressIndicator!
    var testConnectionResultIcon: NSImageView!
    var testConnectionResultLabel: NSTextField!
    var testConnectionDetailsButton: NSButton!
    /// The full (untruncated) error text behind `testConnectionDetailsButton`
    /// — shown in a popover, kept separate from the short inline label.
    var connectionTestFullError: String?

    // MARK: Guided gcloud delete (CloudAccountsWindowController+GuidedDelete.swift)
    // Built fresh on each presentGuidedGcpDeleteSheet call — these just hold
    // the current sheet's controls so its button/field actions (which must
    // be @objc selectors on self) can reach them.

    var guidedDeleteWindow: NSWindow!
    var guidedDeletePopup: NSPopUpButton!
    var guidedDeleteNewNameField: NSTextField!
    var guidedDeleteErrorLabel: NSTextField!
    var guidedDeleteSpinner: NSProgressIndicator!
    var guidedDeleteConfirmButton: NSButton!
    var guidedDeleteCancelButton: NSButton!
    var guidedDeleteTargetName = ""
    /// True once the field only offers "create a new configuration" because
    /// there is no other one to activate instead.
    var guidedDeleteForcedCreate = false

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

    /// Provider owning the currently selected/edited credential entry.
    var selectedProvider = "aws"
    var selectedProfileName: String?
    var fieldRows: [FieldRow] = []
    var lastAutoParsedPaste = ""

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
        configureToolbar()
        rebuildProviderPopup()
        NotificationCenter.default.addObserver(self, selector: #selector(handleProfileDidChange(_:)),
                                                name: .profileDidChange, object: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    deinit {
        NotificationCenter.default.removeObserver(self)
    }

    /// Shows the window and (re)loads its credential-entry data — call every
    /// time the Hub opens this card, so a reopened window is never stale.
    func show() {
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        refreshProfiles()
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

/// One row in the profiles sidebar: a provider group header or a profile.
/// The pre-P1 "TOOLS" subheader + tool rows are gone — Launch Templates is a
/// Hub card now, not a sidebar entry here.
enum SidebarRow {
    case header(provider: String, title: String, count: Int)
    case profile(provider: String, name: String)
}
