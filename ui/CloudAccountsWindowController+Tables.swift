import AppKit

extension CloudAccountsWindowController {
    func numberOfRows(in tableView: NSTableView) -> Int {
        tableView == profilesTable ? sidebarRows.count : displayItems.count
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if tableView == profilesTable {
            guard row >= 0, row < sidebarRows.count else { return nil }
            switch sidebarRows[row] {
            case .header(let provider, let title, let count):
                return providerHeaderCell(tableView, providerID: provider, title: title, count: count)
            case .profile(let provider, let name):
                return profileCell(tableView, providerID: provider, name: name)
            }
        }
        guard row >= 0, row < displayItems.count else { return nil }
        switch displayItems[row] {
        case .header(let sec):
            // Group rows span the full width; provide the header once.
            if tableColumn == nil || tableColumn?.identifier.rawValue == "key" {
                return headerCell(tableView, section: sec)
            }
            return nil
        case .field(let idx):
            return tableColumn?.identifier.rawValue == "key"
                ? keyCell(tableView, fieldIndex: idx)
                : valueCell(tableView, fieldIndex: idx)
        }
    }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool {
        if tableView == profilesTable {
            guard row >= 0, row < sidebarRows.count else { return false }
            if case .header = sidebarRows[row] { return true }
            return false
        }
        guard row >= 0, row < displayItems.count else { return false }
        if case .header = displayItems[row] { return true }
        return false
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        if tableView == profilesTable {
            guard row >= 0, row < sidebarRows.count else { return false }
            switch sidebarRows[row] {
            case .profile(let provider, let name):
                if provider == selectedProvider, name == selectedProfileName { return true }
                return confirmDiscardConnectionChanges()
            case .header: return false
            }
        }
        guard row >= 0, row < displayItems.count else { return true }
        if case .header = displayItems[row] { return false }
        return true
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        if tableView == profilesTable {
            guard row >= 0, row < sidebarRows.count else { return 28 }
            switch sidebarRows[row] {
            case .header: return 26
            case .profile: return 28
            }
        }
        guard row >= 0, row < displayItems.count else { return UI.rowHeight }
        if case .header = displayItems[row] { return 30 }
        return UI.rowHeight
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        if notification.object as? NSTableView == profilesTable {
            guard !isRebuildingSidebar else { return }
            let row = profilesTable.selectedRow
            guard row >= 0, row < sidebarRows.count else {
                // A click on empty space deselected the row — showing the old
                // profile's fields past that point is phantom state.
                if selectedProfileName != nil, !hasUnsavedConnectionChanges() {
                    clearDetailForNoSelection()
                }
                return
            }
            switch sidebarRows[row] {
            case .profile(let provider, let name):
                loadProfile(provider: provider, name: name)
            case .header:
                break
            }
        } else if notification.object as? NSTableView == fieldsTable {
            updateVariablesSummary()
        }
    }

    // MARK: - Cell builders (view-based)

    private func profileCell(_ table: NSTableView, providerID: String, name: String) -> NSView {
        let id = NSUserInterfaceItemIdentifier("profileCell")
        if let reused = table.makeView(withIdentifier: id, owner: self) as? NSTableCellView {
            reused.textField?.stringValue = name
            reused.imageView?.image = ProviderStyle.dot(providerID)
            return reused
        }
        let cell = NSTableCellView()
        cell.identifier = id

        let dot = NSImageView()
        dot.translatesAutoresizingMaskIntoConstraints = false
        cell.addSubview(dot)
        cell.imageView = dot

        let text = NSTextField(labelWithString: name)
        text.font = .systemFont(ofSize: 13)
        text.lineBreakMode = .byTruncatingTail
        text.translatesAutoresizingMaskIntoConstraints = false
        cell.addSubview(text)
        cell.textField = text

        NSLayoutConstraint.activate([
            dot.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 8),
            dot.centerYAnchor.constraint(equalTo: cell.centerYAnchor),
            dot.widthAnchor.constraint(equalToConstant: 7),
            dot.heightAnchor.constraint(equalToConstant: 7),
            text.leadingAnchor.constraint(equalTo: dot.trailingAnchor, constant: 7),
            text.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -6),
            text.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
        ])
        dot.image = ProviderStyle.dot(providerID)
        return cell
    }

    /// Provider group header in the sidebar: brand badge + name + count.
    private func providerHeaderCell(_ table: NSTableView, providerID: String, title: String, count: Int) -> NSView {
        let id = NSUserInterfaceItemIdentifier("providerHeaderCell")
        let cell = (table.makeView(withIdentifier: id, owner: self) as? ProviderHeaderView)
            ?? ProviderHeaderView(reuseIdentifier: id)
        cell.configure(providerID: providerID, title: title, count: count)
        return cell
    }

    private func headerCell(_ table: NSTableView, section: VarSection) -> NSView {
        let id = NSUserInterfaceItemIdentifier("headerCell")
        let cell = (table.makeView(withIdentifier: id, owner: self) as? SectionHeaderView)
            ?? SectionHeaderView(reuseIdentifier: id, target: self, action: #selector(toggleSection(_:)))
        let idxs = fieldRows.indices.filter { self.section(for: fieldRows[$0].key) == section }
        let filled = idxs.filter { !fieldRows[$0].value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.count
        cell.configure(section: section,
                       collapsed: collapsedSections.contains(section),
                       countText: "\(filled)/\(idxs.count)")
        return cell
    }

    /// Keys are stable identifiers → flat labels. Only user-added ("Additional")
    /// keys are editable so a custom variable can be named.
    private func keyCell(_ table: NSTableView, fieldIndex idx: Int) -> NSView {
        let id = NSUserInterfaceItemIdentifier("keyCell")
        let field = (table.makeView(withIdentifier: id, owner: self) as? NSTextField) ?? {
            let f = NSTextField(labelWithString: "")
            f.identifier = id
            f.font = .systemFont(ofSize: 12, weight: .medium)
            f.lineBreakMode = .byTruncatingTail
            f.focusRingType = .none
            f.isBordered = false
            f.drawsBackground = false
            f.target = self
            f.action = #selector(keyFieldEdited(_:))
            return f
        }()
        let key = fieldRows[idx].key
        let isCustom = section(for: key) == .additional
        field.tag = idx
        field.isEditable = isCustom
        field.isSelectable = true
        field.textColor = isCustom ? .labelColor : .secondaryLabelColor
        field.placeholderString = isCustom ? "custom_key" : nil
        field.stringValue = displayKey(key)
        return field
    }

    private func valueCell(_ table: NSTableView, fieldIndex idx: Int) -> NSView {
        let id = NSUserInterfaceItemIdentifier("valueCell")
        let cell = (table.makeView(withIdentifier: id, owner: self) as? FieldValueCellView)
            ?? FieldValueCellView(reuseIdentifier: id,
                                  target: self,
                                  valueAction: #selector(valueFieldEdited(_:)),
                                  eyeAction: #selector(toggleRowReveal(_:)))
        let f = fieldRows[idx]
        cell.configure(row: idx,
                       value: f.value,
                       isSecret: isSecretKey(f.key),
                       revealed: f.revealed,
                       mask: masked(f.value),
                       placeholder: placeholderExample(for: f.key))
        return cell
    }

    // MARK: - Inline edit / reveal handlers (tag = fieldRows index)

    @objc func keyFieldEdited(_ sender: NSTextField) {
        let idx = sender.tag
        guard idx >= 0, idx < fieldRows.count else { return }
        let typed = sender.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if typed == displayKey(fieldRows[idx].key) { return }   // blur without change
        fieldRows[idx].key = typed
        updateVariablesSummary()
        updateProfileMode()
    }

    @objc func valueFieldEdited(_ sender: NSTextField) {
        let idx = sender.tag
        guard idx >= 0, idx < fieldRows.count else { return }
        fieldRows[idx].value = sender.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        updateVariablesSummary()
        updateProfileMode()
    }

    @objc func toggleRowReveal(_ sender: NSButton) {
        let idx = sender.tag
        guard idx >= 0, idx < fieldRows.count else { return }
        fieldRows[idx].revealed.toggle()
        if let disp = displayIndex(ofField: idx) {
            fieldsTable.reloadData(forRowIndexes: IndexSet(integer: disp),
                                   columnIndexes: IndexSet(integersIn: 0..<fieldsTable.numberOfColumns))
        }
    }
}

/// Collapsible section header row (uppercase caption + count + disclosure chevron).
final class SectionHeaderView: NSTableCellView {
    private let chevron = NSButton()
    private let titleLabel = NSTextField(labelWithString: "")
    private let countLabel = NSTextField(labelWithString: "")

    init(reuseIdentifier: NSUserInterfaceItemIdentifier, target: AnyObject, action: Selector) {
        super.init(frame: .zero)
        identifier = reuseIdentifier

        chevron.isBordered = false
        chevron.bezelStyle = .regularSquare
        chevron.imagePosition = .imageOnly
        chevron.contentTintColor = .tertiaryLabelColor
        chevron.target = target
        chevron.action = action
        chevron.translatesAutoresizingMaskIntoConstraints = false
        addSubview(chevron)

        titleLabel.font = .systemFont(ofSize: 11, weight: .semibold)
        titleLabel.textColor = .secondaryLabelColor
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(titleLabel)

        countLabel.font = .systemFont(ofSize: 11, weight: .regular)
        countLabel.textColor = .tertiaryLabelColor
        countLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(countLabel)

        NSLayoutConstraint.activate([
            chevron.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 4),
            chevron.centerYAnchor.constraint(equalTo: centerYAnchor),
            chevron.widthAnchor.constraint(equalToConstant: 16),
            titleLabel.leadingAnchor.constraint(equalTo: chevron.trailingAnchor, constant: 2),
            titleLabel.centerYAnchor.constraint(equalTo: centerYAnchor),
            countLabel.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
            countLabel.centerYAnchor.constraint(equalTo: centerYAnchor)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(section: VarSection, collapsed: Bool, countText: String) {
        chevron.identifier = NSUserInterfaceItemIdentifier(section.rawValue)
        let sym = collapsed ? "chevron.right" : "chevron.down"
        let cfg = NSImage.SymbolConfiguration(pointSize: 10, weight: .semibold)
        chevron.image = NSImage(systemSymbolName: sym, accessibilityDescription: collapsed ? "Expand" : "Collapse")?
            .withSymbolConfiguration(cfg)
        titleLabel.stringValue = section.rawValue.uppercased()
        countLabel.stringValue = countText
    }
}

/// Value cell: a borderless field that shows a subtle rounded "well" only on
/// hover/focus (no permanent bezel), plus a trailing eye toggle for secrets.
final class FieldValueCellView: NSTableCellView {
    private let well = NSView()
    let field = WellTextField()
    private let eyeButton = NSButton()

    init(reuseIdentifier: NSUserInterfaceItemIdentifier, target: AnyObject, valueAction: Selector, eyeAction: Selector) {
        super.init(frame: .zero)
        identifier = reuseIdentifier

        well.wantsLayer = true
        well.layer?.cornerRadius = 5
        well.layer?.borderWidth = 1
        well.isHidden = true
        well.translatesAutoresizingMaskIntoConstraints = false
        addSubview(well)

        field.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        field.isBordered = false
        field.drawsBackground = false
        field.focusRingType = .none
        field.usesSingleLineMode = true
        field.lineBreakMode = .byTruncatingTail
        field.cell?.isScrollable = true
        field.target = target
        field.action = valueAction
        field.onEditingChange = { [weak self] editing in self?.setWellVisible(editing) }
        field.translatesAutoresizingMaskIntoConstraints = false
        addSubview(field)

        eyeButton.isBordered = false
        eyeButton.bezelStyle = .regularSquare
        eyeButton.imagePosition = .imageOnly
        eyeButton.contentTintColor = .tertiaryLabelColor
        eyeButton.target = target
        eyeButton.action = eyeAction
        eyeButton.translatesAutoresizingMaskIntoConstraints = false
        addSubview(eyeButton)

        NSLayoutConstraint.activate([
            field.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 4),
            field.centerYAnchor.constraint(equalTo: centerYAnchor),
            field.trailingAnchor.constraint(equalTo: eyeButton.leadingAnchor, constant: -6),
            eyeButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -10),
            eyeButton.centerYAnchor.constraint(equalTo: centerYAnchor),
            eyeButton.widthAnchor.constraint(equalToConstant: 20),
            eyeButton.heightAnchor.constraint(equalToConstant: 20),
            well.leadingAnchor.constraint(equalTo: field.leadingAnchor, constant: -6),
            well.trailingAnchor.constraint(equalTo: field.trailingAnchor, constant: 6),
            well.topAnchor.constraint(equalTo: centerYAnchor, constant: -12),
            well.bottomAnchor.constraint(equalTo: centerYAnchor, constant: 12)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    /// The well is a focus-only affordance: it appears while the field is being
    /// edited and hides otherwise. (No hover trigger — it read as flickery.)
    private func setWellVisible(_ visible: Bool) {
        well.isHidden = !visible
        well.layer?.backgroundColor = NSColor.controlBackgroundColor.cgColor
        well.layer?.borderColor = (visible ? NSColor.separatorColor : NSColor.clear).cgColor
    }

    func configure(row: Int, value: String, isSecret: Bool, revealed: Bool, mask: String, placeholder: String) {
        field.tag = row
        eyeButton.tag = row
        setWellVisible(false)
        setPlaceholder(placeholder)

        guard isSecret else {
            eyeButton.isHidden = true
            field.isEditable = true
            field.textColor = .labelColor
            field.stringValue = value
            return
        }

        eyeButton.isHidden = false
        let hidden = !revealed
        field.isEditable = !hidden
        field.textColor = hidden ? .secondaryLabelColor : .labelColor
        field.stringValue = hidden ? (value.isEmpty ? "" : mask) : value

        let sym = hidden ? "eye" : "eye.slash"
        let label = hidden ? "Show value" : "Hide value"
        let cfg = NSImage.SymbolConfiguration(pointSize: 13, weight: .regular)
        eyeButton.image = NSImage(systemSymbolName: sym, accessibilityDescription: label)?.withSymbolConfiguration(cfg)
        eyeButton.toolTip = "Click to \(label.lowercased())"
        eyeButton.setAccessibilityLabel(label)
    }

    private func setPlaceholder(_ text: String) {
        field.placeholderAttributedString = NSAttributedString(
            string: text,
            attributes: [.foregroundColor: NSColor.placeholderTextColor,
                         .font: NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)])
    }
}

/// Borderless field that reports when it begins/ends editing, so its cell can
/// show the rounded "well" only while focused.
final class WellTextField: NSTextField {
    var onEditingChange: ((Bool) -> Void)?

    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        if ok { onEditingChange?(true) }
        return ok
    }

    override func textDidEndEditing(_ notification: Notification) {
        super.textDidEndEditing(notification)
        onEditingChange?(false)
    }
}
