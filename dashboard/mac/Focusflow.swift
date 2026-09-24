// Focusflow: a native window around the focusflow dashboard, plus an always-on-top mini board.
// Built by bin/make-app.py (swiftc). The dashboard itself is the local web app; this only hosts it,
// makes sure its server (the launchd job local.focusflow.dashboard) is running, and adds the floating window.
import Cocoa
import WebKit

let label = "local.focusflow.dashboard"
let home = FileManager.default.homeDirectoryForCurrentUser
let configDir = home.appendingPathComponent(".focusflow/dashboard")

func readPort() -> Int {
    if let data = try? Data(contentsOf: configDir.appendingPathComponent("instance.json")),
       let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
       let tunables = json["tunables"] as? [String: Any], let port = tunables["port"] as? Int { return port }
    return 4177
}

func readToken() -> String {
    (try? String(contentsOf: configDir.appendingPathComponent("token"), encoding: .utf8))?
        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
}

// How many things are waiting on the user (review + questions + failed runs), from the local dashboard.
// nil when the server does not answer; the Dock badge then keeps what it had.
func needsBadge(_ port: Int) -> Int? {
    guard let url = URL(string: "http://127.0.0.1:\(port)/api/needs-you") else { return nil }
    var req = URLRequest(url: url, timeoutInterval: 3)
    req.setValue(readToken(), forHTTPHeaderField: "x-focusflow-token")
    let sem = DispatchSemaphore(value: 0)
    var result: Int?
    URLSession.shared.dataTask(with: req) { data, resp, _ in
        defer { sem.signal() }
        guard (resp as? HTTPURLResponse)?.statusCode == 200, let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        result = json["badge"] as? Int
    }.resume()
    _ = sem.wait(timeout: .now() + 4)
    return result
}

func serverUp(_ port: Int) -> Bool {
    let sock = socket(AF_INET, SOCK_STREAM, 0)
    if sock < 0 { return false }
    defer { close(sock) }
    var addr = sockaddr_in()
    addr.sin_family = sa_family_t(AF_INET)
    addr.sin_port = in_port_t(port).bigEndian
    addr.sin_addr.s_addr = inet_addr("127.0.0.1")
    let ok = withUnsafePointer(to: &addr) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(sock, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
    }
    return ok == 0
}

@discardableResult
func launchctl(_ args: [String]) -> Int32 {
    let p = Process()
    p.executableURL = URL(fileURLWithPath: "/bin/launchctl")
    p.arguments = args
    p.standardOutput = FileHandle.nullDevice
    p.standardError = FileHandle.nullDevice
    try? p.run()
    p.waitUntilExit()
    return p.terminationStatus
}

// Starts the dashboard server if it is not answering. Returns true once it is.
func ensureServer(_ port: Int) -> Bool {
    if serverUp(port) { return true }
    let uid = String(getuid())
    let plist = home.appendingPathComponent("Library/LaunchAgents/\(label).plist").path
    if launchctl(["print", "gui/\(uid)/\(label)"]) == 0 {
        launchctl(["kickstart", "-k", "gui/\(uid)/\(label)"])
    } else if FileManager.default.fileExists(atPath: plist) {
        launchctl(["bootstrap", "gui/\(uid)", plist])
    }
    for _ in 0..<40 {
        if serverUp(port) { return true }
        Thread.sleep(forTimeInterval: 0.5)
    }
    return serverUp(port)
}

final class Board: NSObject, WKNavigationDelegate {
    let web: WKWebView
    let mini: Bool
    var retry: Timer?

    init(mini: Bool) {
        self.mini = mini
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = WKWebsiteDataStore.default()
        web = WKWebView(frame: .zero, configuration: cfg)
        super.init()
        web.navigationDelegate = self
        web.setValue(false, forKey: "drawsBackground")
    }

    func url() -> URL {
        let port = readPort()
        let frag = "token=\(readToken())&route=%2Fhome" + (mini ? "&mini=1" : "")
        return URL(string: "http://127.0.0.1:\(port)/#\(frag)")!
    }

    func load() {
        DispatchQueue.global().async {
            let up = ensureServer(readPort())
            DispatchQueue.main.async {
                if up { self.web.load(URLRequest(url: self.url())) } else { self.showDown() }
            }
        }
    }

    func showDown() {
        web.loadHTMLString("<body style='font:15px -apple-system;background:#16181a;color:#e8e6e1;display:flex;height:100vh;align-items:center;justify-content:center;text-align:center'><div><h2>The dashboard is not running</h2><p>Trying again…</p><p style='opacity:.6'>Or run: cd ~/Documents/random/focusflow &amp;&amp; npm start</p></div></body>", baseURL: nil)
        retry?.invalidate()
        retry = Timer.scheduledTimer(withTimeInterval: 5, repeats: false) { [weak self] _ in self?.load() }
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { showDown() }
}

final class App: NSObject, NSApplicationDelegate {
    var main: NSWindow!
    var miniWin: NSPanel!
    let mainBoard = Board(mini: false)
    let miniBoard = Board(mini: true)

    func applicationDidFinishLaunching(_ n: Notification) {
        main = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1320, height: 860),
                        styleMask: [.titled, .closable, .miniaturizable, .resizable],
                        backing: .buffered, defer: false)
        main.title = "Focusflow"
        main.contentView = mainBoard.web
        main.center()
        main.setFrameAutosaveName("FocusflowMain")
        main.makeKeyAndOrderFront(nil)
        mainBoard.load()

        miniWin = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 360, height: 520),
                          styleMask: [.titled, .closable, .resizable, .utilityWindow, .nonactivatingPanel],
                          backing: .buffered, defer: false)
        miniWin.title = "Focusflow — board"
        miniWin.level = .floating
        miniWin.isFloatingPanel = true
        miniWin.hidesOnDeactivate = false
        miniWin.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        miniWin.contentView = miniBoard.web
        miniWin.setFrameAutosaveName("FocusflowMini")
        if !miniWin.setFrameUsingName("FocusflowMini") {
            if let s = NSScreen.main { miniWin.setFrameOrigin(NSPoint(x: s.visibleFrame.maxX - 380, y: s.visibleFrame.maxY - 540)) }
        }
        miniBoard.load()

        buildMenu()
        NSApp.activate(ignoringOtherApps: true)
        Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in self?.keepAlive() }
        updateBadge()
        Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in self?.updateBadge() }
    }

    // The Dock icon shows how many things need you; empty when nothing does.
    func updateBadge() {
        DispatchQueue.global().async {
            guard let n = needsBadge(readPort()) else { return }
            DispatchQueue.main.async { NSApp.dockTile.badgeLabel = n > 0 ? String(n) : nil }
        }
    }

    // A long-running window should recover if the server was restarted under it.
    func keepAlive() {
        DispatchQueue.global().async {
            if !serverUp(readPort()) { _ = ensureServer(readPort()); DispatchQueue.main.async { self.mainBoard.load(); self.miniBoard.load() } }
        }
    }

    func buildMenu() {
        let bar = NSMenu()
        func menu(_ title: String) -> NSMenu {
            let item = NSMenuItem(); bar.addItem(item)
            let m = NSMenu(title: title); item.submenu = m
            return m
        }
        func add(_ m: NSMenu, _ title: String, _ action: Selector, _ key: String, _ mods: NSEvent.ModifierFlags = [.command], target: AnyObject? = nil, tag: Int = 0) {
            let it = NSMenuItem(title: title, action: action, keyEquivalent: key)
            it.keyEquivalentModifierMask = mods
            it.target = target
            it.tag = tag
            m.addItem(it)
        }

        let appMenu = menu("Focusflow")
        add(appMenu, "About Focusflow", #selector(go(_:)), "", target: self, tag: 10)
        appMenu.addItem(.separator())
        add(appMenu, "Settings…", #selector(go(_:)), ",", target: self, tag: 11)
        appMenu.addItem(.separator())
        add(appMenu, "Hide Focusflow", #selector(NSApplication.hide(_:)), "h")
        add(appMenu, "Quit Focusflow", #selector(NSApplication.terminate(_:)), "q")

        let file = menu("File")
        add(file, "New Task", #selector(newTask), "n", target: self)
        add(file, "New Project", #selector(newProject), "n", [.command, .shift], target: self)
        file.addItem(.separator())
        add(file, "Search…", #selector(search), "k", target: self)
        add(file, "Close Window", #selector(NSWindow.performClose(_:)), "w")

        // Without an Edit menu, copy and paste do not reach the web view.
        let edit = menu("Edit")
        add(edit, "Undo", Selector(("undo:")), "z")
        add(edit, "Redo", Selector(("redo:")), "z", [.command, .shift])
        edit.addItem(.separator())
        add(edit, "Cut", #selector(NSText.cut(_:)), "x")
        add(edit, "Copy", #selector(NSText.copy(_:)), "c")
        add(edit, "Paste", #selector(NSText.paste(_:)), "v")
        add(edit, "Select All", #selector(NSText.selectAll(_:)), "a")

        let view = menu("View")
        add(view, "Reload", #selector(reloadAll), "r", target: self)
        add(view, "Show Mini Window", #selector(toggleMini), "m", [.command, .shift], target: self)
        let pin = NSMenuItem(title: "Keep Mini Window on Top", action: #selector(togglePin(_:)), keyEquivalent: ""); pin.target = self; pin.state = .on
        view.addItem(pin)

        let goMenu = menu("Go")
        for (i, name) in ["Home", "Inbox", "Tasks", "Projects", "Memory"].enumerated() {
            add(goMenu, name, #selector(go(_:)), String(i + 1), target: self, tag: i + 1)
        }
        goMenu.addItem(.separator())
        add(goMenu, "Archive", #selector(go(_:)), "", target: self, tag: 6)

        let win = menu("Window")
        add(win, "Minimize", #selector(NSWindow.performMiniaturize(_:)), "m")
        NSApp.windowsMenu = win

        let help = menu("Help")
        add(help, "Focusflow Help", #selector(go(_:)), "?", target: self, tag: 12)
        add(help, "Troubleshooting", #selector(go(_:)), "", target: self, tag: 13)
        add(help, "Keyboard Shortcuts", #selector(go(_:)), "/", target: self, tag: 14)
        NSApp.helpMenu = help
        NSApp.mainMenu = bar
    }

    // The web app routes on its URL fragment; the menus just change it.
    static let routes = [1: "/home", 2: "/inbox", 3: "/tasks", 4: "/projects", 5: "/memory", 6: "/archive",
                         10: "/about", 11: "/settings", 12: "/help/getting-started", 13: "/help/troubleshooting", 14: "/help/shortcuts"]
    @objc func go(_ sender: NSMenuItem) {
        guard let r = App.routes[sender.tag] else { return }
        main.makeKeyAndOrderFront(nil)
        mainBoard.web.evaluateJavaScript("location.hash = '#\(r)'", completionHandler: nil)
    }
    @objc func newTask() { main.makeKeyAndOrderFront(nil); mainBoard.web.evaluateJavaScript("dispatchEvent(new CustomEvent('ff:new-task'))", completionHandler: nil) }
    @objc func newProject() { main.makeKeyAndOrderFront(nil); mainBoard.web.evaluateJavaScript("dispatchEvent(new CustomEvent('ff:new-project'))", completionHandler: nil) }
    @objc func search() { main.makeKeyAndOrderFront(nil); mainBoard.web.evaluateJavaScript("dispatchEvent(new CustomEvent('ff:search'))", completionHandler: nil) }

    @objc func reloadAll() { mainBoard.load(); miniBoard.load() }
    @objc func toggleMini() { if miniWin.isVisible { miniWin.orderOut(nil) } else { miniWin.orderFrontRegardless() } }
    @objc func togglePin(_ s: NSMenuItem) {
        s.state = s.state == .on ? .off : .on
        miniWin.level = s.state == .on ? .floating : .normal
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }
    func applicationShouldHandleReopen(_ app: NSApplication, hasVisibleWindows: Bool) -> Bool {
        if !hasVisibleWindows { main.makeKeyAndOrderFront(nil) }
        return true
    }
}

let app = NSApplication.shared
let delegate = App()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
