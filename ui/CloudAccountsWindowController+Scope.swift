import AppKit

/// The account-scoping editor, opened from this window's "Scope" toolbar
/// item. Per docs/PLATFORM.md principle 5 ("core owns no plugin data"), this
/// is the Cloud Accounts plugin's OWN settings — it used to be the "Accounts"
/// checkbox table in the old Profile Manager, which no longer has one at all
/// (ui/ProfileManagerWindowController.swift). Saving here writes only this
/// plugin's settings blob (CredentialsService.saveCloudAccountsSettings),
/// never the whole profile object.
extension CloudAccountsWindowController {
    @objc func openScopeSheet() {
        let current = profile.cloudAccountsSettings

        let showAllCheckbox = NSButton(checkboxWithTitle: "Show all accounts (ignore the list below)", target: nil, action: nil)
        showAllCheckbox.state = current.showAllAccounts ? .on : .off
        showAllCheckbox.translatesAutoresizingMaskIntoConstraints = false

        // Every (provider, account) already loaded for this window's sidebar
        // — no extra CLI calls needed, refreshProfiles() already has them.
        var allAccounts: [AccountRef] = []
        for info in catalog.providers {
            for summary in profilesByProvider[info.id] ?? [] {
                allAccounts.append(AccountRef(provider: info.id, account: summary.name))
            }
        }

        let table = NSTableView()
        table.headerView = nil
        table.rowHeight = 22
        table.backgroundColor = .clear
        table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("account")))
        let dataSource = ScopeAccountsDataSource(accounts: allAccounts, selected: Set(current.accounts))
        table.dataSource = dataSource
        table.delegate = dataSource
        table.translatesAutoresizingMaskIntoConstraints = false

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        scroll.documentView = table
        scroll.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            scroll.widthAnchor.constraint(equalToConstant: 320),
            scroll.heightAnchor.constraint(equalToConstant: 220)
        ])

        let stack = NSStackView(views: [showAllCheckbox, scroll])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 8
        stack.translatesAutoresizingMaskIntoConstraints = false

        let alert = NSAlert()
        alert.messageText = "Scope this profile's accounts"
        alert.informativeText = "“\(profile.name)” — choose which accounts this window's sidebar shows."
        alert.accessoryView = stack
        alert.addButton(withTitle: "Save")
        alert.addButton(withTitle: "Cancel")
        table.reloadData()
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        let newSettings = CloudAccountsSettings(
            showAllAccounts: showAllCheckbox.state == .on,
            accounts: Array(dataSource.selected)
        )
        do {
            let saved = try service.saveCloudAccountsSettings(profileID: profile.id, newSettings)
            profile = saved
            rebuildSidebarRows()
            // Any other open window on this same profile (the Hub, another
            // Cloud Accounts window) re-fetches and re-renders rather than
            // going stale — same convention every other whole-profile-facing
            // save already uses.
            NotificationCenter.default.post(name: .profileDidChange, object: profile.id)
            setStatus("Updated account scope")
        } catch {
            showError(error.localizedDescription)
        }
    }
}

/// Table data source/delegate for the Scope sheet's account checklist.
final class ScopeAccountsDataSource: NSObject, NSTableViewDataSource, NSTableViewDelegate {
    let accounts: [AccountRef]
    var selected: Set<AccountRef>

    init(accounts: [AccountRef], selected: Set<AccountRef>) {
        self.accounts = accounts
        self.selected = selected
    }

    func numberOfRows(in tableView: NSTableView) -> Int { accounts.count }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        let id = NSUserInterfaceItemIdentifier("scopeAccountCell")
        let cell = (tableView.makeView(withIdentifier: id, owner: self) as? ScopeAccountCheckboxCell)
            ?? ScopeAccountCheckboxCell(reuseIdentifier: id, target: self, action: #selector(toggle(_:)))
        let account = accounts[row]
        cell.configure(row: row, title: "\(account.provider) · \(account.account)", checked: selected.contains(account))
        return cell
    }

    @objc private func toggle(_ sender: NSButton) {
        guard sender.tag >= 0, sender.tag < accounts.count else { return }
        let account = accounts[sender.tag]
        if selected.contains(account) {
            selected.remove(account)
        } else {
            selected.insert(account)
        }
    }
}

/// One Scope-sheet row: a checkbox + "provider · account" label.
final class ScopeAccountCheckboxCell: NSTableCellView {
    private let checkbox = NSButton(checkboxWithTitle: "", target: nil, action: nil)

    init(reuseIdentifier: NSUserInterfaceItemIdentifier, target: AnyObject, action: Selector) {
        super.init(frame: .zero)
        identifier = reuseIdentifier
        checkbox.target = target
        checkbox.action = action
        checkbox.translatesAutoresizingMaskIntoConstraints = false
        addSubview(checkbox)
        NSLayoutConstraint.activate([
            checkbox.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
            checkbox.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -6),
            checkbox.centerYAnchor.constraint(equalTo: centerYAnchor)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(row: Int, title: String, checked: Bool) {
        checkbox.tag = row
        checkbox.title = title
        checkbox.state = checked ? .on : .off
    }
}
