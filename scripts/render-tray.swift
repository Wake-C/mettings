import AppKit
// A template glyph: black ink and transparent cutouts, 18 pt at 2x.
let rep = NSBitmapImageRep(bitmapDataPlanes:nil,pixelsWide:36,pixelsHigh:36,bitsPerSample:8,samplesPerPixel:4,hasAlpha:true,isPlanar:false,colorSpaceName:.deviceRGB,bytesPerRow:0,bitsPerPixel:0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep:rep)
let c=NSGraphicsContext.current!.cgContext
c.translateBy(x:0,y:36);c.scaleBy(x:1,y:-1)
c.setFillColor(NSColor.black.cgColor)
let back=CGMutablePath();back.move(to:CGPoint(x:21,y:2));back.addLine(to:CGPoint(x:29,y:2));back.addQuadCurve(to:CGPoint(x:34,y:7),control:CGPoint(x:34,y:2));back.addLine(to:CGPoint(x:34,y:15));back.addQuadCurve(to:CGPoint(x:30,y:20),control:CGPoint(x:34,y:19));back.addLine(to:CGPoint(x:32,y:24));back.addLine(to:CGPoint(x:27,y:21));back.addLine(to:CGPoint(x:27,y:17));back.addQuadCurve(to:CGPoint(x:18,y:8),control:CGPoint(x:27,y:8));back.addLine(to:CGPoint(x:15,y:8));back.addLine(to:CGPoint(x:15,y:7));back.addQuadCurve(to:CGPoint(x:21,y:2),control:CGPoint(x:15,y:2));back.closeSubpath();c.addPath(back);c.fillPath()
let front=CGMutablePath();front.move(to:CGPoint(x:9,y:10));front.addLine(to:CGPoint(x:19,y:10));front.addQuadCurve(to:CGPoint(x:26,y:17),control:CGPoint(x:26,y:10));front.addLine(to:CGPoint(x:26,y:23));front.addQuadCurve(to:CGPoint(x:19,y:30),control:CGPoint(x:26,y:30));front.addLine(to:CGPoint(x:11,y:30));front.addLine(to:CGPoint(x:4,y:35));front.addLine(to:CGPoint(x:6,y:29));front.addQuadCurve(to:CGPoint(x:2,y:23),control:CGPoint(x:2,y:27));front.addLine(to:CGPoint(x:2,y:17));front.addQuadCurve(to:CGPoint(x:9,y:10),control:CGPoint(x:2,y:10));front.closeSubpath();c.addPath(front);c.fillPath()
c.setBlendMode(.clear)
for rect in [CGRect(x:6.5,y:17.5,width:3,height:7),CGRect(x:12.5,y:14,width:3,height:14),CGRect(x:18.5,y:17.5,width:3,height:7)] {c.addPath(CGPath(roundedRect:rect,cornerWidth:1.5,cornerHeight:1.5,transform:nil));c.fillPath()}
NSGraphicsContext.restoreGraphicsState()
try rep.representation(using:.png,properties:[:])!.write(to:URL(fileURLWithPath:CommandLine.arguments[1]))
