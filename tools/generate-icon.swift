import AppKit
import Foundation

enum IconGeneratorError: LocalizedError {
    case invalidMaster(String)
    case pngEncoding
    case iconutil(Int32)

    var errorDescription: String? {
        switch self {
        case let .invalidMaster(message):
            return message
        case .pngEncoding:
            return "Could not encode PNG"
        case let .iconutil(status):
            return "iconutil failed with status \(status)"
        }
    }
}

let projectURL = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent()
    .deletingLastPathComponent()
let assetsURL = projectURL.appendingPathComponent("assets", isDirectory: true)
let iconBaseName = "EZCloudManagerAppIcon"
let masterURL = assetsURL.appendingPathComponent("\(iconBaseName).master.png")
let iconsetURL = assetsURL.appendingPathComponent("\(iconBaseName).iconset", isDirectory: true)
let icnsURL = assetsURL.appendingPathComponent("\(iconBaseName).icns")
let previewURL = assetsURL.appendingPathComponent("\(iconBaseName).preview.png")

let masterData = try Data(contentsOf: masterURL)
guard let masterRep = NSBitmapImageRep(data: masterData),
      masterRep.pixelsWide == 1024,
      masterRep.pixelsHigh == 1024 else {
    throw IconGeneratorError.invalidMaster(
        "The icon master must be a readable 1024×1024 PNG: \(masterURL.path)"
    )
}

let masterImage = NSImage(size: NSSize(width: 1024, height: 1024))
masterImage.addRepresentation(masterRep)

let fileManager = FileManager.default
try fileManager.createDirectory(at: assetsURL, withIntermediateDirectories: true)
if fileManager.fileExists(atPath: iconsetURL.path) {
    try fileManager.removeItem(at: iconsetURL)
}
try fileManager.createDirectory(at: iconsetURL, withIntermediateDirectories: true)

func drawIcon(pixelSize: Int) -> NSBitmapImageRep {
    let scale = CGFloat(pixelSize) / 1024
    let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: pixelSize,
        pixelsHigh: pixelSize,
        bitsPerSample: 8,
        samplesPerPixel: 4,
        hasAlpha: true,
        isPlanar: false,
        colorSpaceName: .deviceRGB,
        bytesPerRow: 0,
        bitsPerPixel: 0
    )!

    let context = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    context.imageInterpolation = .high
    context.cgContext.setAllowsAntialiasing(true)
    context.cgContext.setShouldAntialias(true)

    NSColor.clear.setFill()
    NSRect(x: 0, y: 0, width: pixelSize, height: pixelSize).fill()

    let iconRect = NSRect(
        x: 40 * scale,
        y: 40 * scale,
        width: 944 * scale,
        height: 944 * scale
    )
    let mask = NSBezierPath(
        roundedRect: iconRect,
        xRadius: 214 * scale,
        yRadius: 214 * scale
    )

    // Slight optical enlargement preserves the mark's cut and outer silhouette
    // in Finder list views without a second hand-edited small-size artwork.
    let crop: CGFloat
    switch pixelSize {
    case ...32:
        crop = 36
    case ...64:
        crop = 20
    default:
        crop = 0
    }
    let sourceRect = NSRect(
        x: crop,
        y: crop,
        width: 1024 - crop * 2,
        height: 1024 - crop * 2
    )

    NSGraphicsContext.saveGraphicsState()
    mask.addClip()
    masterImage.draw(
        in: iconRect,
        from: sourceRect,
        operation: .copy,
        fraction: 1,
        respectFlipped: false,
        hints: nil
    )
    NSGraphicsContext.restoreGraphicsState()

    NSColor.white.withAlphaComponent(0.20).setStroke()
    mask.lineWidth = max(1, 5 * scale)
    mask.stroke()

    context.flushGraphics()
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

func writePNG(_ rep: NSBitmapImageRep, to url: URL) throws {
    guard let data = rep.representation(using: .png, properties: [:]) else {
        throw IconGeneratorError.pngEncoding
    }
    try data.write(to: url, options: .atomic)
}

let iconFiles: [(String, Int)] = [
    ("icon_16x16.png", 16),
    ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32),
    ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128),
    ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256),
    ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512),
    ("icon_512x512@2x.png", 1024)
]

for (fileName, size) in iconFiles {
    try writePNG(drawIcon(pixelSize: size), to: iconsetURL.appendingPathComponent(fileName))
}
try writePNG(drawIcon(pixelSize: 1024), to: previewURL)

// Local verification compares the deterministic PNG representations without
// asking the installed Xcode's iconutil to repackage them. Some Xcode builds
// reject otherwise-valid legacy iconsets; release builds still package by
// default, or explicitly validate the committed ICNS in build.sh's fallback.
if ProcessInfo.processInfo.environment["EZCLOUD_SKIP_ICONUTIL"] == "1" {
    print("Generated PNG representations (iconutil skipped for verification)")
    exit(EXIT_SUCCESS)
}

let iconutil = Process()
iconutil.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
iconutil.arguments = ["-c", "icns", iconsetURL.path, "-o", icnsURL.path]
try iconutil.run()
iconutil.waitUntilExit()

guard iconutil.terminationStatus == 0 else {
    throw IconGeneratorError.iconutil(iconutil.terminationStatus)
}

print("Generated:")
print("  \(icnsURL.path)")
print("  \(previewURL.path)")
