import AppKit

/// Window construction for the Launch Templates editor, split out to keep
/// the controller file focused on behavior (and under the size budget).
extension LaunchTemplatesWindowController {

    func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 980, height: 640),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false)
        win.minSize = NSSize(width: 820, height: 480)
        win.center()
        self.window = win
        let content = NSView()
        win.contentView = content

        regionField = NSTextField(string: "")
        regionField.placeholderString = "us-east-1"
        regionField.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        let loadButton = NSButton(title: "Load", target: self, action: #selector(loadTemplates))
        loadButton.bezelStyle = .rounded

        spinner = NSProgressIndicator()
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false

        statusLabel = NSTextField(labelWithString: "Pick a region and load templates")
        statusLabel.font = .systemFont(ofSize: 11)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail

        let topBar = NSStackView(views: [NSTextField(labelWithString: "Region:"), regionField, loadButton, spinner, statusLabel])
        topBar.orientation = .horizontal
        topBar.spacing = 8
        topBar.translatesAutoresizingMaskIntoConstraints = false
        regionField.widthAnchor.constraint(equalToConstant: 140).isActive = true
        content.addSubview(topBar)

        templatesTable = NSTableView()
        templatesTable.headerView = nil
        templatesTable.delegate = self
        templatesTable.dataSource = self
        templatesTable.style = .sourceList
        templatesTable.backgroundColor = .clear
        templatesTable.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("name")))
        let templatesScroll = NSScrollView()
        templatesScroll.hasVerticalScroller = true
        templatesScroll.drawsBackground = false
        templatesScroll.documentView = templatesTable
        templatesScroll.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(templatesScroll)

        versionPopup = NSPopUpButton()
        versionPopup.target = self
        versionPopup.action = #selector(versionPicked(_:))

        deleteVersionButton = NSButton(title: "Delete Version…", target: self, action: #selector(deleteSelectedVersion))
        deleteVersionButton.bezelStyle = .rounded
        deleteVersionButton.controlSize = .small

        rollbackButton = NSButton(title: "Roll Back Default", target: self, action: #selector(rollback))
        rollbackButton.bezelStyle = .rounded
        rollbackButton.controlSize = .small
        rollbackButton.isHidden = true

        fieldSearch = NSSearchField()
        fieldSearch.placeholderString = "Filter fields"
        fieldSearch.controlSize = .small
        fieldSearch.delegate = self

        let versionBar = NSStackView(views: [NSTextField(labelWithString: "Version:"), versionPopup, deleteVersionButton, rollbackButton, fieldSearch])
        versionBar.orientation = .horizontal
        versionBar.spacing = 8
        versionBar.translatesAutoresizingMaskIntoConstraints = false

        fieldsTable = NSTableView()
        fieldsTable.delegate = self
        fieldsTable.dataSource = self
        fieldsTable.rowHeight = 24
        fieldsTable.usesAlternatingRowBackgroundColors = true
        let keyCol = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("key"))
        keyCol.title = "Field (dotted path)"
        keyCol.width = 340
        let valueCol = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("value"))
        valueCol.title = "Value (orange = edited, empty = remove)"
        valueCol.width = 420
        fieldsTable.addTableColumn(keyCol)
        fieldsTable.addTableColumn(valueCol)
        let fieldsScroll = NSScrollView()
        fieldsScroll.hasVerticalScroller = true
        fieldsScroll.borderType = .bezelBorder
        fieldsScroll.documentView = fieldsTable
        fieldsScroll.translatesAutoresizingMaskIntoConstraints = false

        let addFieldButton = NSButton(title: "Add Field…", target: self, action: #selector(addField))
        addFieldButton.bezelStyle = .rounded
        addFieldButton.controlSize = .small

        descriptionField = NSTextField(string: "")
        descriptionField.placeholderString = "Version description (optional)"

        setDefaultCheckbox = NSButton(checkboxWithTitle: "Set as default version", target: nil, action: nil)
        setDefaultCheckbox.state = .on

        applyButton = NSButton(title: "Apply as New Version", target: self, action: #selector(applyEdits))
        applyButton.bezelStyle = .push
        applyButton.keyEquivalent = "\r"
        applyButton.isEnabled = false

        let bottomBar = NSStackView(views: [addFieldButton, descriptionField, setDefaultCheckbox, applyButton])
        bottomBar.orientation = .horizontal
        bottomBar.spacing = 10
        bottomBar.translatesAutoresizingMaskIntoConstraints = false

        let editor = NSStackView(views: [versionBar, fieldsScroll, bottomBar])
        editor.orientation = .vertical
        editor.alignment = .leading
        editor.spacing = 10
        editor.translatesAutoresizingMaskIntoConstraints = false

        content.addSubview(editor)

        NSLayoutConstraint.activate([
            topBar.topAnchor.constraint(equalTo: content.topAnchor, constant: 12),
            topBar.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            topBar.trailingAnchor.constraint(lessThanOrEqualTo: content.trailingAnchor, constant: -14),

            templatesScroll.topAnchor.constraint(equalTo: topBar.bottomAnchor, constant: 12),
            templatesScroll.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            templatesScroll.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14),
            templatesScroll.widthAnchor.constraint(equalToConstant: 230),

            editor.topAnchor.constraint(equalTo: topBar.bottomAnchor, constant: 12),
            editor.leadingAnchor.constraint(equalTo: templatesScroll.trailingAnchor, constant: 14),
            editor.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            editor.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14),

            fieldsScroll.leadingAnchor.constraint(equalTo: editor.leadingAnchor),
            fieldsScroll.trailingAnchor.constraint(equalTo: editor.trailingAnchor),
            versionBar.trailingAnchor.constraint(lessThanOrEqualTo: editor.trailingAnchor),
            bottomBar.trailingAnchor.constraint(equalTo: editor.trailingAnchor),
            descriptionField.widthAnchor.constraint(greaterThanOrEqualToConstant: 220)
        ])
    }
}
