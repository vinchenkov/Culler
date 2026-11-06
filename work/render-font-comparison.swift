import AppKit

let directory = URL(fileURLWithPath: "/Users/vinchenkov/dev/ai/collie/work")
let width = 1200
let height = 1130
let scale = 2
let image = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: width * scale, pixelsHigh: height * scale, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
let context = NSGraphicsContext(bitmapImageRep: image)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = context
context.cgContext.scaleBy(x: CGFloat(scale), y: CGFloat(scale))

func color(_ value: UInt32) -> NSColor {
    NSColor(srgbRed: CGFloat((value >> 16) & 255) / 255, green: CGFloat((value >> 8) & 255) / 255, blue: CGFloat(value & 255) / 255, alpha: 1)
}

func rectangle(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat, _ radius: CGFloat, _ fill: UInt32, _ stroke: UInt32? = nil) {
    let path = NSBezierPath(roundedRect: NSRect(x: x, y: CGFloat(height) - y - h, width: w, height: h), xRadius: radius, yRadius: radius)
    color(fill).setFill()
    path.fill()
    if let stroke {
        color(stroke).setStroke()
        path.lineWidth = 1
        path.stroke()
    }
}

func text(_ value: String, _ x: CGFloat, _ y: CGFloat, _ font: NSFont, _ ink: UInt32 = 0x244363) {
    let attributes: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: color(ink)]
    let size = (value as NSString).size(withAttributes: attributes)
    (value as NSString).draw(at: NSPoint(x: x, y: CGFloat(height) - y - size.height), withAttributes: attributes)
}

let choices = [
    ("Avenir Next", "AvenirNext-Medium", "Geometric"),
    ("Helvetica Neue", "HelveticaNeue-Medium", "Neutral"),
    ("Futura", "Futura-Medium", "Rounded"),
    ("Menlo", "Menlo-Regular", "Monospace"),
    ("DIN Alternate", "DINAlternate-Bold", "Narrow")
]
let roles = ["Strategist", "Arbiter", "Worker", "Verifier", "Packager"]
let purposes = ["Proposes two tasks", "Selects proposed tasks", "Implements a task", "Checks the deliverable", "Prepares the review packet"]
let ui = NSFont(name: "AvenirNext-Regular", size: 13)!
let label = NSFont(name: "AvenirNext-DemiBold", size: 16)!

rectangle(0, 0, CGFloat(width), CGFloat(height), 0, 0xf4ecdd)
text("Pipeline number fonts", 30, 23, NSFont(name: "AvenirNext-DemiBold", size: 25)!)
text("Each preview uses 32-pixel numbers. The card text stays the same.", 30, 60, ui, 0x53667a)

for (index, choice) in choices.enumerated() {
    guard let font = NSFont(name: choice.1, size: 32) else { fatalError("The font is unavailable: \(choice.1)") }
    print("\(choice.0): \(font.fontName)")
    let y = CGFloat(100 + index * 201)
    text(choice.0, 30, y, label)
    text(choice.2, 240, y + 3, ui, 0x53667a)
    for column in 0..<5 {
        let x = CGFloat(30 + column * 230)
        let top = y + 32
        let active = column == 2
        rectangle(x, top, 218, 145, 12, active ? 0xe4e9ed : 0xffffff, active ? 0x92a8bd : 0xdce2e8)
        if active { rectangle(x + 10, top, 198, 3, 1, 0x385e83) }
        text(String(format: "%02d", column + 1), x + 16, top + 14, font, 0x53667a)
        rectangle(x + 182, top + 19, 18, 25, 10, active ? 0x9fc4e8 : 0xf4ecdd, 0x385e83)
        text(roles[column], x + 16, top + 62, label)
        text(purposes[column], x + 16, top + 88, ui, 0x53667a)
        text(active ? "●  Agent launch pending" : "●  Idle", x + 16, top + 116, ui, active ? 0x385e83 : 0x244363)
    }
}
NSGraphicsContext.restoreGraphicsState()
let destination = directory.appendingPathComponent("pipeline-font-comparison.png")
try image.representation(using: .png, properties: [:])!.write(to: destination)
print(destination.path)
