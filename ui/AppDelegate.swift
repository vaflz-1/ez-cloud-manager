import AppKit
import Foundation

/// AppDelegate owns the window and app state. Its behavior is split across
/// focused extensions to keep each concern readable:
///   - AppDelegate+Menu       — main menu construction
///   - AppDelegate+Layout     — window / sidebar / detail view building
///   - AppDelegate+Tables     — NSTableView data source & delegate
///   - AppDelegate+Actions    — user-triggered actions (add/save/delete/parse)
///   - AppDelegate+State      — profile loading, field rows, status helpers
///   - AppDelegate+Transfer   — export / import / compare / activate
///   - AppDelegate+Workspaces — workspace switching & membership
///
/// Members are `internal` (not `private`) because they are shared across those
/// extension files; the app is a single-module binary, so nothing leaks.
final class AppDelegate: NSObject, NSApplicationDelegate, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate, NSSearchFieldDelegate, NSTextViewDelegate, NSToolbarDelegate {
    var window: NSWindow!
    var addRemoveControl: NSSegmentedControl!
    var profilesTable: NSTableView!
    var fieldsTable: NSTableView!
    var profileNameField: NSTextField!
    var profileModeLabel: NSTextField!
    var providerPopup: NSPopUpButton!
    var workspacePopup: NSPopUpButton!
    var pasteView: NSTextView!
    var pasteLabel: NSTextField!
    var statusLabel: NSTextField!
    var variablesTitleLabel: NSTextField!
    var variablesSummaryLabel: NSTextField!
    var saveButton: NSButton!
    var activateButton: NSButton!
    var exportButton: NSPopUpButton!
    var profileSearchField: NSSearchField!

    /// Boundary to the `ezcloud` CLI. All persistence goes through this.
    let service = CredentialsService()

    // MARK: Multi-provider state

    /// Registered backends, in canonical display order (aws, gcp, azure, …).
    var providers: [ProviderInfo] = []
    /// Field catalogs per provider id — drive labels, masking, placeholders.
    var schemas: [String: ProviderSchema] = [:]
    /// Full profile lists per provider; the sidebar renders a filtered view.
    var profilesByProvider: [String: [ProfileSummary]] = [:]
    /// Storage path per provider (shown in status / delete confirmations).
    var pathsByProvider: [String: String] = [:]
    /// The rendered sidebar (headers + profiles after search/workspace filter).
    var sidebarRows: [SidebarRow] = []

    var workspaces: [Workspace] = []
    /// nil = "All Profiles" (no workspace filter).
    var activeWorkspaceName: String?
    /// True while rebuildSidebarRows() reloads the table — reloadData fires
    /// spurious selection-change notifications that must not clear the detail.
    var isRebuildingSidebar = false
    /// Profile whose selection was hidden by the current filter; restored
    /// automatically the moment the filter lets it back in.
    var hiddenSelection: (provider: String, name: String)?
    /// 3px accent stripe on the PROFILE card, tinted by the editing provider.
    var profileCardStripe: NSView!

    /// Provider owning the currently selected/edited profile.
    var selectedProvider = "aws"
    var selectedProfileName: String?
    var fieldRows: [FieldRow] = []
    var lastAutoParsedPaste = ""

    /// Retains the Launch Templates window while it is open.
    var launchTemplatesController: LaunchTemplatesWindowController?

    /// Presentation layer over `fieldRows`: section headers + field references,
    /// so the flat model drives grouped, collapsible rows (Common/Advanced/…).
    var displayItems: [VarItem] = []
    var collapsedSections: Set<VarSection> = []

    static let koFiURL = URL(string: "https://ko-fi.com/vaflz")!

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        configureMainMenu()
        buildWindow()
        configureToolbar()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        loadProviders()
        reloadWorkspaces()
        refreshProfiles()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
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

/// One row in the profiles sidebar: a provider group header, a profile, a
/// "TOOLS" subheader, or one of the provider's tools/services.
enum SidebarRow {
    case header(provider: String, title: String, count: Int)
    case profile(provider: String, name: String)
    case subheader(String)
    case tool(provider: String, id: String, title: String, symbol: String)
}
