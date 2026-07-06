import AppKit
import Foundation

let projectURL = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent()
    .deletingLastPathComponent()
let assetsURL = projectURL.appendingPathComponent("assets", isDirectory: true)
let iconsetURL = assetsURL.appendingPathComponent("EZCloudManagerCloudGear.iconset", isDirectory: true)
let icnsURL = assetsURL.appendingPathComponent("EZCloudManagerCloudGear.icns")
let previewURL = assetsURL.appendingPathComponent("EZCloudManagerCloudGear.preview.png")

let fileManager = FileManager.default
try fileManager.createDirectory(at: assetsURL, withIntermediateDirectories: true)
if fileManager.fileExists(atPath: iconsetURL.path) {
    try fileManager.removeItem(at: iconsetURL)
}
try fileManager.createDirectory(at: iconsetURL, withIntermediateDirectories: true)

func color(_ red: CGFloat, _ green: CGFloat, _ blue: CGFloat, _ alpha: CGFloat = 1) -> NSColor {
    NSColor(calibratedRed: red, green: green, blue: blue, alpha: alpha)
}

func drawCircle(center: CGPoint, radius: CGFloat, fill: NSColor, stroke: NSColor? = nil, lineWidth: CGFloat = 1) {
    let rect = NSRect(x: center.x - radius, y: center.y - radius, width: radius * 2, height: radius * 2)
    let path = NSBezierPath(ovalIn: rect)
    fill.setFill()
    path.fill()
    if let stroke {
        stroke.setStroke()
        path.lineWidth = lineWidth
        path.stroke()
    }
}

func pipeline(points: [CGPoint], width: CGFloat, stroke: NSColor) {
    guard let first = points.first else { return }

    let path = NSBezierPath()
    path.move(to: first)
    for point in points.dropFirst() {
        path.line(to: point)
    }
    path.lineWidth = width
    path.lineCapStyle = .round
    path.lineJoinStyle = .round
    stroke.setStroke()
    path.stroke()
}

func drawSymbol(_ name: String, in rect: NSRect, pointScale: CGFloat = 0.86, color symbolColor: NSColor, shadow: NSShadow? = nil) {
    guard let base = NSImage(systemSymbolName: name, accessibilityDescription: nil) else {
        return
    }

    let sizeConfig = NSImage.SymbolConfiguration(pointSize: rect.height * pointScale, weight: .regular)
    let colorConfig = NSImage.SymbolConfiguration(paletteColors: [symbolColor])
    let symbol = base.withSymbolConfiguration(sizeConfig.applying(colorConfig)) ?? base

    NSGraphicsContext.saveGraphicsState()
    shadow?.set()
    symbol.draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: false, hints: nil)
    NSGraphicsContext.restoreGraphicsState()
}

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

    func p(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
        CGPoint(x: x * scale, y: y * scale)
    }

    func r(_ x: CGFloat, _ y: CGFloat, _ width: CGFloat, _ height: CGFloat) -> NSRect {
        NSRect(x: x * scale, y: y * scale, width: width * scale, height: height * scale)
    }

    NSGraphicsContext.saveGraphicsState()
    let context = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = context
    context.cgContext.setAllowsAntialiasing(true)
    context.cgContext.setShouldAntialias(true)

    NSColor.clear.setFill()
    r(0, 0, 1024, 1024).fill()

    let background = NSBezierPath(roundedRect: r(40, 40, 944, 944), xRadius: 214 * scale, yRadius: 214 * scale)
    let gradient = NSGradient(colors: [
        color(0.66, 0.89, 0.99),
        color(0.21, 0.55, 0.82),
        color(0.18, 0.25, 0.47)
    ])!
    gradient.draw(in: background, angle: 315)

    NSGraphicsContext.saveGraphicsState()
    let backgroundStroke = background.copy() as! NSBezierPath
    color(1.0, 1.0, 1.0, 0.34).setStroke()
    backgroundStroke.lineWidth = 8 * scale
    backgroundStroke.stroke()
    NSGraphicsContext.restoreGraphicsState()

    pipeline(points: [p(150, 282), p(300, 282), p(300, 392), p(452, 392)], width: 12 * scale, stroke: color(0.91, 0.98, 1.0, 0.56))
    pipeline(points: [p(186, 748), p(326, 748), p(326, 674), p(450, 674)], width: 11 * scale, stroke: color(0.90, 0.96, 1.0, 0.50))

    for (center, radius, fill) in [
        (p(150, 282), 22 * scale, color(0.90, 0.98, 1.0)),
        (p(300, 282), 16 * scale, color(0.90, 0.97, 1.0)),
        (p(452, 392), 19 * scale, color(0.90, 0.98, 1.0)),
        (p(186, 748), 19 * scale, color(0.90, 0.97, 1.0)),
        (p(326, 674), 15 * scale, color(0.89, 0.98, 1.0))
    ] {
        drawCircle(center: center, radius: radius, fill: fill, stroke: color(0.08, 0.18, 0.31, 0.34), lineWidth: 5 * scale)
    }

    let cloudShadow = NSShadow()
    cloudShadow.shadowColor = NSColor.black.withAlphaComponent(0.22)
    cloudShadow.shadowBlurRadius = 34 * scale
    cloudShadow.shadowOffset = NSSize(width: 0, height: -12 * scale)
    drawSymbol("cloud.fill", in: r(205, 318, 610, 390), pointScale: 0.96, color: color(0.78, 0.94, 1.0, 0.70), shadow: nil)
    drawSymbol("cloud.fill", in: r(210, 328, 598, 380), pointScale: 0.96, color: color(0.98, 1.0, 1.0, 0.94), shadow: cloudShadow)

    pipeline(points: [p(370, 535), p(478, 535), p(522, 576), p(616, 576)], width: 9 * scale, stroke: color(0.20, 0.48, 0.68, 0.58))
    pipeline(points: [p(420, 474), p(544, 474), p(544, 526), p(662, 526)], width: 8 * scale, stroke: color(0.20, 0.48, 0.68, 0.48))
    for center in [p(370, 535), p(522, 576), p(616, 576), p(420, 474), p(544, 526), p(662, 526)] {
        drawCircle(center: center, radius: 14 * scale, fill: color(0.14, 0.43, 0.64, 0.88), stroke: color(1.0, 1.0, 1.0, 0.88), lineWidth: 4 * scale)
    }

    let plateShadow = NSShadow()
    plateShadow.shadowColor = NSColor.black.withAlphaComponent(0.24)
    plateShadow.shadowBlurRadius = 24 * scale
    plateShadow.shadowOffset = NSSize(width: 0, height: -8 * scale)

    NSGraphicsContext.saveGraphicsState()
    plateShadow.set()
    let gearPlate = NSBezierPath(ovalIn: r(596, 292, 292, 292))
    let plateGradient = NSGradient(colors: [
        color(1.0, 1.0, 1.0, 0.98),
        color(0.86, 0.91, 0.97, 0.96)
    ])!
    plateGradient.draw(in: gearPlate, angle: 90)
    NSGraphicsContext.restoreGraphicsState()

    color(1.0, 1.0, 1.0, 0.70).setStroke()
    gearPlate.lineWidth = 6 * scale
    gearPlate.stroke()

    let gearShadow = NSShadow()
    gearShadow.shadowColor = NSColor.black.withAlphaComponent(0.16)
    gearShadow.shadowBlurRadius = 10 * scale
    gearShadow.shadowOffset = NSSize(width: 0, height: -3 * scale)
    drawSymbol("gearshape.fill", in: r(640, 336, 204, 204), pointScale: 0.98, color: color(0.26, 0.35, 0.46, 0.96), shadow: gearShadow)

    context.flushGraphics()
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

func writePNG(_ rep: NSBitmapImageRep, to url: URL) throws {
    guard let data = rep.representation(using: .png, properties: [:]) else {
        throw NSError(domain: "IconGenerator", code: 1, userInfo: [NSLocalizedDescriptionKey: "Could not encode PNG"])
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

let iconutil = Process()
iconutil.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
iconutil.arguments = ["-c", "icns", iconsetURL.path, "-o", icnsURL.path]
try iconutil.run()
iconutil.waitUntilExit()

if iconutil.terminationStatus != 0 {
    throw NSError(domain: "IconGenerator", code: Int(iconutil.terminationStatus), userInfo: [NSLocalizedDescriptionKey: "iconutil failed"])
}

print("Generated:")
print("  \(icnsURL.path)")
print("  \(previewURL.path)")
