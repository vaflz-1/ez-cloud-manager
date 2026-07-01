import AppKit

// Application entry point. Top-level executable code must live in `main.swift`.
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
