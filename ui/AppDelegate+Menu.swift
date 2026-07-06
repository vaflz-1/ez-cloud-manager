import AppKit

extension AppDelegate {
    func configureMainMenu() {
        let mainMenu = NSMenu()

        let appMenuItem = NSMenuItem()
        mainMenu.addItem(appMenuItem)
        let appMenu = NSMenu()
        appMenuItem.submenu = appMenu
        appMenu.addItem(NSMenuItem(title: "About EZ Cloud Manager", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: ""))
        appMenu.addItem(.separator())
        appMenu.addItem(NSMenuItem(title: "Hide EZ Cloud Manager", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h"))
        appMenu.addItem(NSMenuItem(title: "Hide Others", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h"))
        appMenu.items.last?.keyEquivalentModifierMask = [.command, .option]
        appMenu.addItem(NSMenuItem(title: "Show All", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: ""))
        appMenu.addItem(.separator())
        appMenu.addItem(NSMenuItem(title: "Quit EZ Cloud Manager", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"))

        let fileMenuItem = NSMenuItem()
        mainMenu.addItem(fileMenuItem)
        let fileMenu = NSMenu(title: "File")
        fileMenuItem.submenu = fileMenu

        // Profile (the new global container) management — app-global, so
        // these targets stay `self` (AppDelegate).
        let newWindowItem = NSMenuItem(title: "New Window with Profile", action: nil, keyEquivalent: "")
        let newWindowSubmenu = NSMenu()
        newWindowSubmenu.delegate = self
        newWindowItem.submenu = newWindowSubmenu
        fileMenu.addItem(newWindowItem)
        let manageItem = NSMenuItem(title: "Manage Profiles…", action: #selector(manageProfiles), keyEquivalent: ",")
        manageItem.target = self
        fileMenu.addItem(manageItem)
        fileMenu.addItem(.separator())

        // Everything below acts on ONE profile window's credential-entry
        // editor. `target = nil` routes each command through the responder
        // chain (key window → its ProfileWindowController) instead of a
        // fixed target — required now that these actions live on
        // ProfileWindowController, not AppDelegate.
        let newItem = NSMenuItem(title: "New Profile", action: #selector(ProfileWindowController.addProfile), keyEquivalent: "n")
        newItem.target = nil
        fileMenu.addItem(newItem)
        let saveItem = NSMenuItem(title: "Save Profile", action: #selector(ProfileWindowController.saveProfile), keyEquivalent: "s")
        saveItem.target = nil
        fileMenu.addItem(saveItem)
        let refreshItem = NSMenuItem(title: "Refresh Profiles", action: #selector(ProfileWindowController.refreshTapped), keyEquivalent: "r")
        refreshItem.target = nil
        fileMenu.addItem(refreshItem)
        fileMenu.addItem(.separator())
        let importItem = NSMenuItem(title: "Import File…", action: #selector(ProfileWindowController.importFromFile), keyEquivalent: "i")
        importItem.target = nil
        fileMenu.addItem(importItem)
        let exportItem = NSMenuItem(title: "Export to File…", action: #selector(ProfileWindowController.exportToFile), keyEquivalent: "e")
        exportItem.target = nil
        fileMenu.addItem(exportItem)
        let compareItem = NSMenuItem(title: "Compare Profiles…", action: #selector(ProfileWindowController.compareProfiles), keyEquivalent: "d")
        compareItem.target = nil
        fileMenu.addItem(compareItem)
        fileMenu.addItem(.separator())
        let ltItem = NSMenuItem(title: "EC2 Launch Templates…", action: #selector(ProfileWindowController.openLaunchTemplates), keyEquivalent: "l")
        ltItem.target = nil
        fileMenu.addItem(ltItem)
        fileMenu.addItem(.separator())
        fileMenu.addItem(NSMenuItem(title: "Close", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w"))

        let editMenuItem = NSMenuItem()
        mainMenu.addItem(editMenuItem)
        let editMenu = NSMenu(title: "Edit")
        editMenuItem.submenu = editMenu
        editMenu.addItem(NSMenuItem(title: "Undo", action: Selector(("undo:")), keyEquivalent: "z"))
        editMenu.addItem(NSMenuItem(title: "Redo", action: Selector(("redo:")), keyEquivalent: "Z"))
        editMenu.addItem(.separator())
        editMenu.addItem(NSMenuItem(title: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x"))
        editMenu.addItem(NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c"))
        editMenu.addItem(NSMenuItem(title: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v"))
        editMenu.addItem(NSMenuItem(title: "Delete", action: #selector(NSText.delete(_:)), keyEquivalent: ""))
        editMenu.addItem(.separator())
        editMenu.addItem(NSMenuItem(title: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a"))

        let helpMenuItem = NSMenuItem()
        mainMenu.addItem(helpMenuItem)
        let helpMenu = NSMenu(title: "Help")
        helpMenuItem.submenu = helpMenu
        let koFiItem = NSMenuItem(title: "Support EZ Cloud Manager on Ko-fi ♥", action: #selector(ProfileWindowController.openKoFi), keyEquivalent: "")
        koFiItem.target = nil
        helpMenu.addItem(koFiItem)

        NSApp.mainMenu = mainMenu
    }
}

/// Rebuilds the "New Window with Profile" submenu live every time the File
/// menu opens, so it always reflects the current profile list — the same
/// dynamic-menu pattern the old workspace popup used.
extension AppDelegate: NSMenuDelegate {
    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        guard let profiles = try? service.listProfiles(), !profiles.isEmpty else {
            let hint = NSMenuItem(title: "No profiles yet — use Manage Profiles…", action: nil, keyEquivalent: "")
            hint.isEnabled = false
            menu.addItem(hint)
            return
        }
        for p in profiles.sorted(by: { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }) {
            let item = NSMenuItem(title: p.name, action: #selector(newWindowForProfile(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = p.id
            if windowControllers[p.id] != nil {
                item.state = .on
            }
            menu.addItem(item)
        }
    }
}
