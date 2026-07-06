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
        let newItem = NSMenuItem(title: "New Profile", action: #selector(addProfile), keyEquivalent: "n")
        newItem.target = self
        fileMenu.addItem(newItem)
        let saveItem = NSMenuItem(title: "Save Profile", action: #selector(saveProfile), keyEquivalent: "s")
        saveItem.target = self
        fileMenu.addItem(saveItem)
        let refreshItem = NSMenuItem(title: "Refresh Profiles", action: #selector(refreshTapped), keyEquivalent: "r")
        refreshItem.target = self
        fileMenu.addItem(refreshItem)
        fileMenu.addItem(.separator())
        let importItem = NSMenuItem(title: "Import File…", action: #selector(importFromFile), keyEquivalent: "i")
        importItem.target = self
        fileMenu.addItem(importItem)
        let exportItem = NSMenuItem(title: "Export to File…", action: #selector(exportToFile), keyEquivalent: "e")
        exportItem.target = self
        fileMenu.addItem(exportItem)
        let compareItem = NSMenuItem(title: "Compare Profiles…", action: #selector(compareProfiles), keyEquivalent: "d")
        compareItem.target = self
        fileMenu.addItem(compareItem)
        fileMenu.addItem(.separator())
        let ltItem = NSMenuItem(title: "EC2 Launch Templates…", action: #selector(openLaunchTemplates), keyEquivalent: "l")
        ltItem.target = self
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
        let koFiItem = NSMenuItem(title: "Support EZ Cloud Manager on Ko-fi ♥", action: #selector(openKoFi), keyEquivalent: "")
        koFiItem.target = self
        helpMenu.addItem(koFiItem)

        NSApp.mainMenu = mainMenu
    }
}
