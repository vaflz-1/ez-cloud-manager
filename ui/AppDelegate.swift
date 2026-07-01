import AppKit
import Foundation

/// AppDelegate owns the window and app state. Its behavior is split across
/// focused extensions to keep each concern readable:
///   - AppDelegate+Menu    — main menu construction
///   - AppDelegate+Layout  — window / sidebar / detail view building
///   - AppDelegate+Tables  — NSTableView data source & delegate
///   - AppDelegate+Actions — user-triggered actions (add/save/delete/parse)
///   - AppDelegate+State   — profile loading, field rows, status helpers
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
    var pasteView: NSTextView!
    var statusLabel: NSTextField!
    var variablesTitleLabel: NSTextField!
    var variablesSummaryLabel: NSTextField!
    var variablesEditorView: NSView!
    var saveButton: NSButton!
    var profileSearchField: NSSearchField!

    /// Boundary to the `ezcloud` CLI. All persistence goes through this.
    let service = CredentialsService()

    var credentialsPath = ""
    /// Full profile list from disk; `profiles` is the (search-)filtered view.
    var allProfiles: [ProfileSummary] = []
    var profiles: [ProfileSummary] = []
    var fieldRows: [FieldRow] = []
    var selectedProfileName: String?
    var lastAutoParsedPaste = ""
    let minimumVariablesEditorHeight: CGFloat = 240
    let minimumVariablesTableHeight: CGFloat = 180

    /// Presentation layer over `fieldRows`: section headers + field references,
    /// so the flat model drives grouped, collapsible rows (Common/Advanced/…).
    var displayItems: [VarItem] = []
    var collapsedSections: Set<VarSection> = []

    /// Keys whose values are hidden in the UI unless revealed via the eye toggle.
    let secretKeys: Set<String> = ["aws_secret_access_key", "aws_session_token"]

    /// Fields shown in the always-visible "Common" section (the rest are Advanced).
    let commonKeys: Set<String> = [
        "aws_access_key_id", "aws_secret_access_key", "aws_session_token", "region", "output"
    ]

    let recommendedKeys = [
        "aws_access_key_id",
        "aws_secret_access_key",
        "aws_session_token",
        "region",
        "output",
        "role_arn",
        "source_profile",
        "mfa_serial",
        "duration_seconds",
        "credential_process",
        "credential_source",
        "role_session_name",
        "external_id",
        "web_identity_token_file",
        "endpoint_url",
        "cli_pager",
        "retry_mode",
        "max_attempts",
        "sts_regional_endpoints",
        "sso_session",
        "sso_start_url",
        "sso_region",
        "sso_account_id",
        "sso_role_name"
    ]

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        configureMainMenu()
        buildWindow()
        configureToolbar()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
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
