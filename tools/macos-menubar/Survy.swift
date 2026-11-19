import Cocoa
import WebKit

let url = URL(string: ProcessInfo.processInfo.environment["SURVY_URL"] ?? "https://survy.home.somesan.de/")!

private struct MenuBarConfig: Decodable {
    let unifiURL: String

    static func loadUniFiURL() -> URL? {
        let file = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Survy/config.json")
        do {
            let config = try JSONDecoder().decode(Self.self, from: Data(contentsOf: file))
            guard let url = URL(string: config.unifiURL),
                  let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
                  let host = url.host, !host.isEmpty else {
                NSLog("Survy: unifiURL in %@ must be an absolute HTTP or HTTPS URL.", file.path)
                return nil
            }
            return url
        } catch {
            NSLog("Survy: could not load %@: %@", file.path, String(describing: error))
            return nil
        }
    }
}

class AppDelegate: NSObject, NSApplicationDelegate, WKUIDelegate {
    private let unifiURL = MenuBarConfig.loadUniFiURL()
    var item: NSStatusItem!
    var window: NSWindow?
    var unifiWindow: NSWindow?

    func applicationDidFinishLaunching(_ n: Notification) {
        buildMenu()
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let b = item.button {
            let img = NSImage(systemSymbolName: "chart.bar.doc.horizontal", accessibilityDescription: "Survy")
            img?.isTemplate = true
            b.image = img
            b.toolTip = "Open Survy dashboard"
            b.target = self
            b.action = #selector(clicked(_:))
            b.sendAction(on: [.leftMouseUp, .rightMouseUp])
        }
    }

    func buildMenu() {
        let main = NSMenu()
        let appItem = NSMenuItem(); main.addItem(appItem)
        let appMenu = NSMenu(); appItem.submenu = appMenu
        appMenu.addItem(withTitle: "Quit Survy", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

        let editItem = NSMenuItem(); main.addItem(editItem)
        let edit = NSMenu(title: "Edit"); editItem.submenu = edit
        edit.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")

        let viewItem = NSMenuItem(); main.addItem(viewItem)
        let view = NSMenu(title: "View"); viewItem.submenu = view
        view.addItem(withTitle: "Reload", action: #selector(reload), keyEquivalent: "r")

        let winItem = NSMenuItem(); main.addItem(winItem)
        let win = NSMenu(title: "Window"); winItem.submenu = win
        win.addItem(withTitle: "Close", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        win.addItem(withTitle: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        NSApp.mainMenu = main
    }

    @objc func reload() { (NSApp.keyWindow?.contentView as? WKWebView)?.reload() }

    @objc func openUniFi() {
        guard let unifiURL = unifiURL else { return }
        unifiWindow = showWindow(url: unifiURL, existing: unifiWindow, frameName: "SurvyUniFiWindow")
    }

    func showWindow() {
        window = showWindow(url: url, existing: window, frameName: "SurvyWindow")
    }

    private func showWindow(url: URL, existing: NSWindow?, frameName: String) -> NSWindow {
        let window: NSWindow
        if let existing = existing {
            window = existing
        } else {
            let cfg = WKWebViewConfiguration()
            cfg.websiteDataStore = .default()
            let webView = WKWebView(frame: .zero, configuration: cfg)
            webView.uiDelegate = self
            webView.allowsBackForwardNavigationGestures = true
            webView.load(URLRequest(url: url))

            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1200, height: 800),
                             styleMask: [.titled, .closable, .miniaturizable, .resizable],
                             backing: .buffered, defer: false)
            w.titleVisibility = .hidden
            w.titlebarAppearsTransparent = true
            w.isReleasedWhenClosed = false
            w.contentView = webView
            w.setFrameAutosaveName(frameName)
            if !w.setFrameUsingName(frameName) { w.center() }
            window = w
        }
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
        return window
    }

    // Open target=_blank links in the same window
    func webView(_ webView: WKWebView, createWebViewWith c: WKWebViewConfiguration,
                 for a: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if a.targetFrame == nil { webView.load(a.request) }
        return nil
    }

    @objc func clicked(_ sender: NSStatusBarButton) {
        if NSApp.currentEvent?.type == .rightMouseUp {
            let m = NSMenu()
            let openUniFiItem = m.addItem(withTitle: "Open UniFi Network",
                                         action: unifiURL == nil ? nil : #selector(openUniFi),
                                         keyEquivalent: "")
            openUniFiItem.target = self
            m.addItem(.separator())
            m.addItem(withTitle: "Quit Survy", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
            item.menu = m
            item.button?.performClick(nil)
            item.menu = nil
        } else {
            showWindow()
        }
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let d = AppDelegate()
app.delegate = d
app.run()
