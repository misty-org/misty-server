// Renders the workflow eval's fixture screens with the system WebKit:
//   swift render.swift <page.html> <out.png> && sips -z 800 1280 <out.png>
// WebKit renders at the display's scale; sips brings each PNG to 1280 x 800 pixels.
import AppKit
import WebKit

let args = CommandLine.arguments
guard args.count == 3 else {
    FileHandle.standardError.write("usage: swift render.swift <page.html> <out.png>\n".data(using: .utf8)!)
    exit(2)
}
let page = URL(fileURLWithPath: args[1])
let out = URL(fileURLWithPath: args[2])
let size = NSSize(width: 1280, height: 800)

final class Renderer: NSObject, WKNavigationDelegate {
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        // Let layout and fonts settle before the snapshot.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            let config = WKSnapshotConfiguration()
            config.rect = CGRect(origin: .zero, size: size)
            config.snapshotWidth = NSNumber(value: Double(size.width))
            webView.takeSnapshot(with: config) { image, error in
                guard let image, let tiff = image.tiffRepresentation, let bitmap = NSBitmapImageRep(data: tiff),
                      let png = bitmap.representation(using: .png, properties: [:]) else {
                    FileHandle.standardError.write("snapshot failed: \(String(describing: error))\n".data(using: .utf8)!)
                    exit(1)
                }
                try! png.write(to: out)
                exit(0)
            }
        }
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.prohibited)
let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
let webView = WKWebView(frame: NSRect(origin: .zero, size: size))
let renderer = Renderer()
webView.navigationDelegate = renderer
window.contentView = webView
webView.loadFileURL(page, allowingReadAccessTo: page.deletingLastPathComponent())
app.run()
