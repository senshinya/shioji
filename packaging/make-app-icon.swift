// Convert the approved icon tile into standard macOS ICNS source sizes.
// The original AI concept is retained unchanged beside the generated icon.
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

let root = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
let source = root.appendingPathComponent("Shioji-source.png")
guard let input = CGImageSourceCreateWithURL(source as CFURL, nil), let image = CGImageSourceCreateImageAtIndex(input, 0, nil) else { fatalError("Cannot read the selected icon") }
let set = root.appendingPathComponent("Shioji.iconset", isDirectory: true)
try FileManager.default.createDirectory(at: set, withIntermediateDirectories: true)
for size in [16,32,128,256,512] {
    for scale in [1,2] {
        let pixels = size * scale
        let color = CGColorSpace(name: CGColorSpace.sRGB)!
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
