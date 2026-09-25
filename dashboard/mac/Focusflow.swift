// Focusflow: a native window around the focusflow dashboard, plus an always-on-top mini board.
// Built by bin/make-app.py (swiftc). The dashboard itself is the local web app; this only hosts it,
// makes sure its server (the launchd job local.focusflow.dashboard) is running, and adds the floating window.
import Cocoa
import WebKit
import UserNotifications
import Carbon.HIToolbox

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

// One item that needs the user, as /api/needs-you reports it.
struct Need { let key: String; let fp: String; let kind: String; let slug: String; let name: String; let detail: String; let urgent: Bool; let project: String }
struct NeedsSnapshot { let badge: Int; let total: Int; let items: [Need]; let notifyEnabled: Bool; let quiet: Bool }

// What needs the user right now. nil when the server does not answer (everything keeps what it had).
// The header tells the server this app shows notifications itself, so it does not send its own.
func fetchNeeds(_ port: Int) -> NeedsSnapshot? {
    guard let url = URL(string: "http://127.0.0.1:\(port)/api/needs-you") else { return nil }
    var req = URLRequest(url: url, timeoutInterval: 3)
    req.setValue(readToken(), forHTTPHeaderField: "x-focusflow-token")
    req.setValue("mac-app", forHTTPHeaderField: "x-focusflow-client")
    let sem = DispatchSemaphore(value: 0)
    var result: NeedsSnapshot?
    URLSession.shared.dataTask(with: req) { data, resp, _ in
        defer { sem.signal() }
        guard (resp as? HTTPURLResponse)?.statusCode == 200, let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        let items = (json["items"] as? [[String: Any]] ?? []).map { i in
            Need(key: i["key"] as? String ?? "", fp: i["fp"] as? String ?? "", kind: i["kind"] as? String ?? "",
                 slug: i["slug"] as? String ?? "", name: i["name"] as? String ?? "", detail: i["detail"] as? String ?? "",
                 urgent: i["urgent"] as? Bool ?? false, project: i["project"] as? String ?? "")
        }
        let notify = json["notify"] as? [String: Any] ?? [:]
        result = NeedsSnapshot(badge: json["badge"] as? Int ?? 0, total: json["total"] as? Int ?? items.count, items: items,
                               notifyEnabled: notify["enabled"] as? Bool ?? true, quiet: notify["quiet"] as? Bool ?? false)
    }.resume()
    _ = sem.wait(timeout: .now() + 4)
    return result
}

// Fire-and-forget POST to the local server (notification actions).
func postJSON(_ path: String, _ body: [String: Any]) {
    guard let url = URL(string: "http://127.0.0.1:\(readPort())\(path)"), let data = try? JSONSerialization.data(withJSONObject: body) else { return }
    var req = URLRequest(url: url, timeoutInterval: 30)
    req.httpMethod = "POST"
    req.httpBody = data
    req.setValue("application/json", forHTTPHeaderField: "content-type")
    req.setValue(readToken(), forHTTPHeaderField: "x-focusflow-token")
    URLSession.shared.dataTask(with: req).resume()
}

let kindLabel = ["question": "Question for you", "review": "Ready for your review", "failed": "A background run stopped",
                 "stalled": "Might be stuck", "overdue": "Overdue", "waiting": "Waiting on someone"]

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

final class Board: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
    let web: WKWebView
    let mini: Bool
    let route: String
    var retry: Timer?
    var onClose: (() -> Void)?

    init(mini: Bool, route: String = "%2Fhome") {
        self.mini = mini
        self.route = route
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = WKWebsiteDataStore.default()
        web = WKWebView(frame: .zero, configuration: cfg)
        super.init()
        cfg.userContentController.add(self, name: "focusflow")
        web.navigationDelegate = self
        web.setValue(false, forKey: "drawsBackground")
    }

    // The page asks to close its window (the quick-capture window, after adding a task or on Esc).
    func userContentController(_ c: WKUserContentController, didReceive message: WKScriptMessage) {
        if (message.body as? String) == "close" { onClose?() }
    }

    func url() -> URL {
        let port = readPort()
        let frag = "token=\(readToken())&route=\(route)" + (mini ? "&mini=1" : "")
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
        web.loadHTMLString("<body style='font:15px -apple-system;background:#fbfbfa;color:#202020;display:flex;height:100vh;align-items:center;justify-content:center;text-align:center'><div><h2 style='font-weight:600'>Focusflow is starting…</h2><p style='color:#6c6b66'>Trying again in a moment.</p><p style='color:#8d8b85;font-size:13px'>If this does not go away, run <code>focusflow dashboard start</code> in Terminal.</p></div></body>", baseURL: nil)
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
    let quickBoard = Board(mini: false, route: "%2Fquick")
    var quickWin: NSPanel!
    var statusItem: NSStatusItem!
    let notices = Notices()

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
        setupQuickWindow()
        setupStatusItem()
        notices.app = self
        notices.setup()
        registerQuickHotKey { [weak self] in self?.showQuick() }
        NSApp.activate(ignoringOtherApps: true)
        Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in self?.keepAlive() }
        poll()
        Timer.scheduledTimer(withTimeInterval: 10, repeats: true) { [weak self] _ in self?.poll() }
    }

    // Every few seconds: the Dock badge, the menu-bar item and notifications follow what needs the user.
    func poll() {
        DispatchQueue.global().async {
            guard let snap = fetchNeeds(readPort()) else { return }
            DispatchQueue.main.async {
                NSApp.dockTile.badgeLabel = snap.badge > 0 ? String(snap.badge) : nil
                self.updateStatusItem(snap)
                self.notices.consider(snap)
            }
        }
    }

    func openRoute(_ route: String) {
        NSApp.activate(ignoringOtherApps: true)
        main.makeKeyAndOrderFront(nil)
        mainBoard.web.evaluateJavaScript("location.hash = '#\(route)'", completionHandler: nil)
    }

    // ---- quick capture: a small floating window from anywhere (Control-Option-N) ----
    func setupQuickWindow() {
        quickWin = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 620, height: 96),
                           styleMask: [.titled, .closable, .fullSizeContentView],
                           backing: .buffered, defer: false)
        quickWin.titleVisibility = .hidden
        quickWin.titlebarAppearsTransparent = true
        quickWin.isMovableByWindowBackground = true
        quickWin.level = .floating
        quickWin.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        quickWin.contentView = quickBoard.web
        quickBoard.onClose = { [weak self] in self?.quickWin.orderOut(nil) }
    }

    @objc func showQuick() {
        if let s = NSScreen.main {
            let f = s.visibleFrame
            quickWin.setFrameOrigin(NSPoint(x: f.midX - 310, y: f.maxY - f.height * 0.28))
        }
        quickBoard.load()
        NSApp.activate(ignoringOtherApps: true)
        quickWin.makeKeyAndOrderFront(nil)
    }

    // ---- menu bar ----
    func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.image = NSImage(systemSymbolName: "checkmark.circle", accessibilityDescription: "Focusflow")
        statusItem.button?.imagePosition = .imageLeading
        updateStatusItem(NeedsSnapshot(badge: 0, total: 0, items: [], notifyEnabled: true, quiet: false))
    }

    func updateStatusItem(_ snap: NeedsSnapshot) {
        statusItem.button?.title = snap.total > 0 ? " \(snap.total)" : ""
        statusItem.button?.image = NSImage(systemSymbolName: snap.total > 0 ? "circle.inset.filled" : "checkmark.circle", accessibilityDescription: "Focusflow")
        let m = NSMenu()
        let head = NSMenuItem(title: snap.total == 0 ? "You’re all caught up" : "\(snap.total) thing\(snap.total == 1 ? "" : "s") need\(snap.total == 1 ? "s" : "") you", action: nil, keyEquivalent: "")
        head.isEnabled = false
        m.addItem(head)
        for i in snap.items.prefix(8) {
            let it = NSMenuItem(title: "\(kindLabel[i.kind] ?? i.kind): \(i.name)", action: #selector(openItem(_:)), keyEquivalent: "")
            it.target = self
            it.representedObject = i.slug
            m.addItem(it)
        }
        m.addItem(.separator())
        let nt = NSMenuItem(title: "New Task…", action: #selector(showQuick), keyEquivalent: "n")
        nt.keyEquivalentModifierMask = [.control, .option]
        nt.target = self
        m.addItem(nt)
        let open = NSMenuItem(title: "Open Focusflow", action: #selector(openMain), keyEquivalent: ""); open.target = self
        m.addItem(open)
        m.addItem(.separator())
        m.addItem(withTitle: "Quit Focusflow", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "")
        statusItem.menu = m
    }

    @objc func openItem(_ sender: NSMenuItem) { if let slug = sender.representedObject as? String { openRoute("/task/\(slug)") } }
    @objc func openMain() { NSApp.activate(ignoringOtherApps: true); main.makeKeyAndOrderFront(nil) }

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

// ---- notifications: native, clickable, with Reply and Approve ----
final class Notices: NSObject, UNUserNotificationCenterDelegate {
    weak var app: App?
    private var seen = Set<String>()
    private var primed = false
    private var held = 0
    private let worthABanner: Set<String> = ["question", "review", "failed", "stalled"]

    func setup() {
        let c = UNUserNotificationCenter.current()
        c.delegate = self
        let reply = UNTextInputNotificationAction(identifier: "reply", title: "Reply", options: [], textInputButtonTitle: "Send", textInputPlaceholder: "Your answer…")
        let approve = UNNotificationAction(identifier: "approve", title: "Approve", options: [])
        let open = UNNotificationAction(identifier: "open", title: "Open", options: [.foreground])
        c.setNotificationCategories([
            UNNotificationCategory(identifier: "question", actions: [reply, open], intentIdentifiers: [], options: []),
            UNNotificationCategory(identifier: "review", actions: [approve, open], intentIdentifiers: [], options: []),
            UNNotificationCategory(identifier: "other", actions: [open], intentIdentifiers: [], options: []),
        ])
        c.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    // New items since the last look get a banner; the first look after launch only takes stock.
    // During quiet hours only urgent ones do; the rest arrive as one summary afterwards.
    func consider(_ snap: NeedsSnapshot) {
        let items = snap.items.filter { worthABanner.contains($0.kind) }
        let fresh = items.filter { !seen.contains($0.key + "|" + $0.fp) }
        for i in items { seen.insert(i.key + "|" + i.fp) }
        if !primed { primed = true; return }
        guard snap.notifyEnabled else { return }
        for i in fresh {
            if snap.quiet && !i.urgent { held += 1; continue }
            post(i)
        }
        if !snap.quiet && held > 0 {
            let c = UNMutableNotificationContent()
            c.title = "While things were quiet"
            c.body = "\(held) update\(held == 1 ? "" : "s") need\(held == 1 ? "s" : "") you."
            c.categoryIdentifier = "other"
            c.userInfo = ["slug": ""]
            UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: "summary-\(Date().timeIntervalSince1970)", content: c, trigger: nil))
            held = 0
        }
    }

    private func post(_ i: Need) {
        let c = UNMutableNotificationContent()
        c.title = (i.urgent ? "Urgent · " : "") + (kindLabel[i.kind] ?? "Focusflow")
        c.subtitle = i.name
        c.body = i.kind == "review" ? "Claude says this is finished." : i.detail
        c.sound = .default
        c.categoryIdentifier = i.kind == "question" ? "question" : i.kind == "review" ? "review" : "other"
        c.userInfo = ["slug": i.slug]
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: i.key + "|" + i.fp, content: c, trigger: nil))
    }

    func userNotificationCenter(_ c: UNUserNotificationCenter, willPresent n: UNNotification,
                                withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .list, .sound])
    }

    func userNotificationCenter(_ c: UNUserNotificationCenter, didReceive r: UNNotificationResponse,
                                withCompletionHandler done: @escaping () -> Void) {
        let slug = r.notification.request.content.userInfo["slug"] as? String ?? ""
        switch r.actionIdentifier {
        case "reply":
            if let text = (r as? UNTextInputNotificationResponse)?.userText.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty, !slug.isEmpty {
                postJSON("/api/tasks/\(slug)/reply", ["text": text])
            }
        case "approve":
            if !slug.isEmpty { postJSON("/api/tasks/\(slug)/action", ["action": "approve"]) }
        default:
            DispatchQueue.main.async { self.app?.openRoute(slug.isEmpty ? "/home" : "/task/\(slug)") }
        }
        done()
    }
}

// ---- the global hotkey (Control-Option-N): Carbon hotkeys need no Accessibility permission ----
var quickHotKeyAction: (() -> Void)?
var quickHotKeyRef: EventHotKeyRef?
func registerQuickHotKey(_ action: @escaping () -> Void) {
    quickHotKeyAction = action
    var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
    InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
        DispatchQueue.main.async { quickHotKeyAction?() }
        return noErr
    }, 1, &spec, nil, nil)
    let id = EventHotKeyID(signature: OSType(0x46464C57), id: 1) // "FFLW"
    RegisterEventHotKey(UInt32(kVK_ANSI_N), UInt32(controlKey | optionKey), id, GetApplicationEventTarget(), 0, &quickHotKeyRef)
}

let app = NSApplication.shared
let delegate = App()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
