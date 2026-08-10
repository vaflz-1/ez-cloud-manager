import AppKit

extension AppDelegate {
    func configureMainMenu() {
        let mainMenu = NSMenu()

        let appMenuItem = NSMenuItem(title: Product.name, action: nil, keyEquivalent: "")
        mainMenu.addItem(appMenuItem)
        let appMenu = NSMenu()
        appMenuItem.submenu = appMenu
        appMenu.addItem(NSMenuItem(title: "About \(Product.name)", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: ""))
        appMenu.addItem(.separator())
        appMenu.addItem(NSMenuItem(title: "Hide \(Product.name)", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h"))
        appMenu.addItem(NSMenuItem(title: "Hide Others", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h"))
        appMenu.items.last?.keyEquivalentModifierMask = [.command, .option]
        appMenu.addItem(NSMenuItem(title: "Show All", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: ""))
        appMenu.addItem(.separator())
        let appearanceItem = NSMenuItem(title: "Appearance", action: nil, keyEquivalent: "")
        let appearanceMenu = NSMenu(title: "Appearance")
        let selectedAppearance = AppAppearance(
            rawValue: UserDefaults.standard.integer(forKey: "KervikAppearance")
        ) ?? .system
        for choice in AppAppearance.allCases {
            let item = NSMenuItem(title: choice.title, action: #selector(chooseAppearance(_:)), keyEquivalent: "")
            item.target = self
            item.tag = choice.rawValue
            item.state = choice == selectedAppearance ? .on : .off
            appearanceMenu.addItem(item)
        }
        appearanceItem.submenu = appearanceMenu
        appMenu.addItem(appearanceItem)
        appMenu.addItem(.separator())
        appMenu.addItem(NSMenuItem(title: "Quit \(Product.name)", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"))

        // File: just profile-window plumbing now (docs/PLATFORM.md phase P1
        // "menu bar is minimal and basic" — every action that used to live
        // here (New/Save/Refresh/Import/Export/Compare Profile, EC2 Launch
        // Templates…) is reachable from a Hub card or its own plugin window
        // instead; menus mirror the interface, they never hold exclusive
        // functionality).
        let fileMenuItem = NSMenuItem()
        mainMenu.addItem(fileMenuItem)
        let fileMenu = NSMenu(title: "File")
        fileMenuItem.submenu = fileMenu

        let newWindowItem = NSMenuItem(title: "New Window with Workspace", action: nil, keyEquivalent: "")
        let newWindowSubmenu = NSMenu()
        newWindowSubmenu.delegate = self
        newWindowItem.submenu = newWindowSubmenu
        fileMenu.addItem(newWindowItem)
        let refreshItem = NSMenuItem(title: "Refresh Workspace State", action: #selector(refreshWorkspaceState(_:)), keyEquivalent: "r")
        refreshItem.target = self
        fileMenu.addItem(refreshItem)
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

        let windowMenuItem = NSMenuItem()
        mainMenu.addItem(windowMenuItem)
        let windowMenu = NSMenu(title: "Window")
        windowMenuItem.submenu = windowMenu
        windowMenu.addItem(NSMenuItem(title: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m"))
        windowMenu.addItem(NSMenuItem(title: "Zoom", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: ""))
        windowMenu.addItem(.separator())
        windowMenu.addItem(NSMenuItem(title: "Bring All to Front", action: #selector(NSApplication.arrangeInFront(_:)), keyEquivalent: ""))
        NSApp.windowsMenu = windowMenu

        let helpMenuItem = NSMenuItem()
        mainMenu.addItem(helpMenuItem)
        let helpMenu = NSMenu(title: "Help")
        helpMenuItem.submenu = helpMenu
        // Fixed target now (was `nil`/responder-chain routed to whichever
        // ProfileWindowController was key) — with 4+ window classes in play
        // post-P1, that trick got fragile. Every Hub keeps its own toolbar
        // Ko-fi button as a small, deliberate duplicate (see AppDelegate's
        // openKoFi doc comment).
        let koFiItem = NSMenuItem(title: "Support \(Product.name) on Ko-fi", action: #selector(openKoFi), keyEquivalent: "")
        koFiItem.target = self
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
            let hint = NSMenuItem(title: "No workspaces yet — use Manage Workspaces…", action: nil, keyEquivalent: "")
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
