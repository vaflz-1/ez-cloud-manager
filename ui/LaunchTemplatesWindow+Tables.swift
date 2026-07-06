import AppKit

/// NSTableView data source/delegate plumbing for both tables (the template
/// list + the fields editor) plus their small action handlers — split out to
/// keep the controller file under the size budget, mirroring
/// CloudAccountsWindowController+Tables.swift's own split.
extension LaunchTemplatesWindowController {
    func numberOfRows(in tableView: NSTableView) -> Int {
        tableView == templatesTable ? templates.count : visibleKeys.count
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if tableView == templatesTable {
            guard row < templates.count else { return nil }
            let template = templates[row]
            let id = NSUserInterfaceItemIdentifier("ltCell")
            let cell = (tableView.makeView(withIdentifier: id, owner: self) as? NSTableCellView) ?? {
                let c = NSTableCellView(); c.identifier = id
                let t = NSTextField(labelWithString: ""); t.font = .systemFont(ofSize: 12)
                t.lineBreakMode = .byTruncatingTail
                t.translatesAutoresizingMaskIntoConstraints = false
                c.addSubview(t); c.textField = t
                NSLayoutConstraint.activate([
                    t.leadingAnchor.constraint(equalTo: c.leadingAnchor, constant: 8),
                    t.trailingAnchor.constraint(equalTo: c.trailingAnchor, constant: -6),
                    t.centerYAnchor.constraint(equalTo: c.centerYAnchor)])
                return c
            }()
            cell.textField?.stringValue = template.name
            cell.toolTip = "\(template.id) · default v\(template.defaultVersion) · latest v\(template.latestVersion)"
            return cell
        }

        guard row < visibleKeys.count else { return nil }
        let key = visibleKeys[row]
        let isKeyColumn = tableColumn?.identifier.rawValue == "key"
        let id = NSUserInterfaceItemIdentifier(isKeyColumn ? "ltKey" : "ltValue")
        let field = (tableView.makeView(withIdentifier: id, owner: self) as? NSTextField) ?? {
            let f = NSTextField(string: "")
            f.identifier = id
            f.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
            f.isBordered = false
            f.drawsBackground = false
            f.focusRingType = .none
            f.usesSingleLineMode = true
            f.lineBreakMode = .byTruncatingTail
            f.cell?.isScrollable = true
            if !isKeyColumn {
                f.target = self
                f.action = #selector(self.valueEdited(_:))
            }
            return f
        }()
        if isKeyColumn {
            field.isEditable = false
            field.textColor = originalFlat[key] == nil ? .systemBlue : .secondaryLabelColor
            field.stringValue = key
        } else {
            field.isEditable = true
            let value = editedFlat[key] ?? ""
            field.textColor = (originalFlat[key] ?? "") != value ? .systemOrange : .labelColor
            field.stringValue = value
            field.tag = row
        }
        return field
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard notification.object as? NSTableView == templatesTable else { return }
        let row = templatesTable.selectedRow
        guard row >= 0, row < templates.count else { return }
        rollbackVersion = nil
        currentTemplate = templates[row]
        loadVersions(for: templates[row])
    }

    @objc func valueEdited(_ sender: NSTextField) {
        let row = sender.tag
        guard row >= 0, row < visibleKeys.count else { return }
        editedFlat[visibleKeys[row]] = sender.stringValue
        updateApplyState()
        fieldsTable.reloadData(forRowIndexes: IndexSet(integer: row), columnIndexes: IndexSet(integersIn: 0..<2))
    }

    @objc func versionPicked(_ sender: NSPopUpButton) {
        guard let template = currentTemplate,
              let version = sender.selectedItem?.representedObject as? String else { return }
        loadVersionData(template: template, version: version)
    }

    @objc func addField() {
        let alert = NSAlert()
        alert.messageText = "Add field"
        alert.informativeText = "Dotted path into LaunchTemplateData, e.g. InstanceType or TagSpecifications[0].Tags[0].Value"
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        field.placeholderString = "InstanceType"
        alert.accessoryView = field
        alert.addButton(withTitle: "Add")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let key = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !key.isEmpty else { return }
        editedFlat[key] = editedFlat[key] ?? ""
        applyFieldFilter()
        updateApplyState()
    }

    func controlTextDidChange(_ obj: Notification) {
        if obj.object as? NSSearchField == fieldSearch {
            applyFieldFilter()
        }
    }
}
