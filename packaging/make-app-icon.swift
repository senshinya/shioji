// Convert the approved icon tile into the macOS or iOS app icon resources.
// The original AI concept is retained unchanged beside the generated icon.
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

let root = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
let source = root.appendingPathComponent("Shioji-source.png")
guard let input = CGImageSourceCreateWithURL(source as CFURL, nil), let image = CGImageSourceCreateImageAtIndex(input, 0, nil) else { fatalError("Cannot read the selected icon") }
let color = CGColorSpace(name: CGColorSpace.sRGB)!

if CommandLine.arguments.dropFirst(2).contains("--ios") {
    let pixels = 1024
    let cropSize = 870
    let crop = CGRect(x: (image.width - cropSize) / 2, y: (image.height - cropSize) / 2,
                      width: cropSize, height: cropSize)
    guard let tile = image.cropping(to: crop),
          let context = CGContext(data: nil, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: 0,
                                  space: color, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue),
          let buffer = context.data?.assumingMemoryBound(to: UInt8.self) else {
        fatalError("Cannot render the iOS icon tile")
    }
    context.interpolationQuality = .high
    context.draw(tile, in: CGRect(x: 0, y: 0, width: pixels, height: pixels))
    // Anchor the graphite tone at Home Screen sizes. Fade the adjustment
    // through the shoreline edge so the ivory and antialiasing stay intact.
    for y in 0..<pixels {
        for x in 0..<pixels {
            let offset = y * context.bytesPerRow + 4 * x
            let luma = 0.2126 * Double(buffer[offset]) + 0.7152 * Double(buffer[offset + 1])
                     + 0.0722 * Double(buffer[offset + 2])
            let weight = max(0, min(1, (156 - luma) / 72))
            let tone = 1 - 0.16 * weight * weight * (3 - 2 * weight)
            for channel in offset..<(offset + 3) {
                buffer[channel] = UInt8((Double(buffer[channel]) * tone).rounded())
            }
        }
    }
    let url = root.appendingPathComponent("Shioji-iOS.png")
    let output = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(output, context.makeImage()!, nil)
    guard CGImageDestinationFinalize(output) else { fatalError("Cannot encode the iOS icon") }
    exit(0)
}

let set = root.appendingPathComponent("Shioji.iconset", isDirectory: true)
try FileManager.default.createDirectory(at: set, withIntermediateDirectories: true)
for size in [16,32,128,256,512] {
    for scale in [1,2] {
        let pixels = size * scale
        let context = CGContext(data:nil,width:pixels,height:pixels,bitsPerComponent:8,bytesPerRow:0,space:color,bitmapInfo:CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.scaleBy(x:CGFloat(pixels)/1024,y:CGFloat(pixels)/1024)
        // Native app-icon silhouette, excluding the concept sheet's backdrop.
        context.addPath(CGPath(roundedRect:CGRect(x:95,y:96,width:832,height:832),cornerWidth:166,cornerHeight:166,transform:nil))
        context.clip()
        context.interpolationQuality = .high
        context.draw(image,in:CGRect(x:0,y:0,width:1024,height:1024))
        let filename = "icon_\(size)x\(size)" + (scale == 2 ? "@2x" : "") + ".png"
        let output = CGImageDestinationCreateWithURL(set.appendingPathComponent(filename) as CFURL,UTType.png.identifier as CFString,1,nil)!
        CGImageDestinationAddImage(output,context.makeImage()!,nil)
        guard CGImageDestinationFinalize(output) else { fatalError("Cannot encode icon size") }
    }
}
