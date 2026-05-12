import AppKit
import Foundation
import Carbon.HIToolbox

// MARK: - Config

let kAPIBase        = "http://localhost:8091"
let kIntakeEndpoint = kAPIBase + "/v1/intake"

let kDefaultKeyCode:  UInt32 = 3      // 'f'
let kDefaultMods:     UInt32 = UInt32(optionKey | shiftKey)
let kUDKeyCode    = "fragHotkeyKeyCode"
let kUDModifiers  = "fragHotkeyModifiers"

// MARK: - Palette

// Cyan  — oklch(0.82 0.13 200) → sRGB
let cCyan   = NSColor(srgbRed: 0.2054, green: 0.8666, blue:  0.8986, alpha: 1)
// Magenta — oklch(0.72 0.22 340) → sRGB
let cMag    = NSColor(srgbRed: 0.9758, green: 0.3773, blue:  0.8115, alpha: 1)
let cBg     = NSColor(srgbRed: 0.0549, green: 0.0392, blue:  0.0784, alpha: 1) // #0e0a14
let cBody   = NSColor(srgbRed: 0.9098, green: 0.9569, blue:  0.9725, alpha: 1) // #e8f4f8
let cSep    = NSColor(srgbRed: 0.2275, green: 0.2078, blue:  0.2824, alpha: 1) // #3a3548
let cDim    = NSColor(srgbRed: 0.2902, green: 0.2667, blue:  0.3451, alpha: 1) // #4a4458
let cSec    = NSColor(srgbRed: 0.4784, green: 0.4510, blue:  0.5333, alpha: 1) // #7a7388

// MARK: - Global hotkey state

var gHotkeyRef:    EventHotKeyRef?
var gAppDelegate:  AppDelegate?

let hotkeyHandler: EventHandlerUPP = { _, _, _ -> OSStatus in
    DispatchQueue.main.async { gAppDelegate?.toggleWindow() }
    return noErr
}

// MARK: - AppDelegate

class AppDelegate: NSObject, NSApplicationDelegate {
    var statusItem: NSStatusItem!
    var windowController: FragWindowController?
    var eventHandlerRef: EventHandlerRef?

    func applicationDidFinishLaunching(_ n: Notification) {
        NSApp.setActivationPolicy(.accessory)
        setupStatusItem()
        registerHotkey()
    }

    func applicationWillTerminate(_ n: Notification) { unregisterHotkey() }

    // MARK: Status item

    func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let btn = statusItem.button {
            btn.image = NSImage(systemSymbolName: "square.and.pencil",
                                accessibilityDescription: "Frag")
            btn.image?.isTemplate = true
            btn.sendAction(on: [.leftMouseUp, .rightMouseUp])
            btn.action = #selector(statusClicked(_:))
            btn.target = self
        }
    }

    @objc func statusClicked(_ sender: NSStatusBarButton) {
        if NSApp.currentEvent?.type == .rightMouseUp {
            let m = NSMenu()
            m.addItem(NSMenuItem(title: "Show Frag  \(currentHotkeyLabel())",
                                 action: #selector(showWindow), keyEquivalent: ""))
            m.addItem(.separator())
            m.addItem(NSMenuItem(title: "Set Hotkey…",
                                 action: #selector(presentSetHotkey), keyEquivalent: ""))
            m.addItem(.separator())
            m.addItem(NSMenuItem(title: "Quit", action: #selector(quit), keyEquivalent: "q"))
            statusItem.menu = m
            statusItem.button?.performClick(nil)
            statusItem.menu = nil
        } else {
            toggleWindow()
        }
    }

    // MARK: Window

    @objc func toggleWindow() {
        if let wc = windowController, wc.window?.isVisible == true {
            wc.hideWindow()
        } else {
            showWindow()
        }
    }

    @objc func showWindow() {
        if windowController == nil {
            windowController = FragWindowController()
            windowController?.onSetHotkey = { [weak self] in self?.presentSetHotkey() }
        }
        windowController?.showWindow()
    }

    @objc func quit() { NSApp.terminate(nil) }

    // MARK: Hotkey registration

    func registerHotkey() {
        unregisterHotkey()
        var et = EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                               eventKind:  OSType(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(),
                            hotkeyHandler, 1, &et, nil, &eventHandlerRef)
        let hkID = EventHotKeyID(signature: OSType(0x46524147), id: 1)
        RegisterEventHotKey(savedKeyCode(), savedModifiers(), hkID,
                            GetApplicationEventTarget(), 0, &gHotkeyRef)
    }

    func unregisterHotkey() {
        if let r = gHotkeyRef   { UnregisterEventHotKey(r); gHotkeyRef = nil }
        if let r = eventHandlerRef { RemoveEventHandler(r); eventHandlerRef = nil }
    }

    // MARK: Set-hotkey sheet

    @objc func presentSetHotkey() {
        let alert = NSAlert()
        alert.messageText    = "Set Global Hotkey"
        alert.informativeText = "Click the field then press your key combo.\nCurrent: \(currentHotkeyLabel())   Default: ⌥⇧F"
        alert.addButton(withTitle: "Save")
        alert.addButton(withTitle: "Reset to Default")
        alert.addButton(withTitle: "Cancel")

        let field = HotkeyTextField(frame: NSRect(x: 0, y: 0, width: 220, height: 24))
        field.placeholderString = "Click here, then press keys…"
        field.isBordered = true; field.bezelStyle = .roundedBezel
        alert.accessoryView = field

        windowController?.suppressHide = true
        NSApp.activate(ignoringOtherApps: true)
        let r = alert.runModal()
        windowController?.suppressHide = false
        if r == .alertFirstButtonReturn, field.capturedKeyCode != 0 {
            saveHotkey(keyCode: field.capturedKeyCode, modifiers: field.capturedModifiers)
            registerHotkey()
            windowController?.updateHotkeyLabel(currentHotkeyLabel())
        } else if r == .alertSecondButtonReturn {
            saveHotkey(keyCode: kDefaultKeyCode, modifiers: kDefaultMods)
            registerHotkey()
            windowController?.updateHotkeyLabel(currentHotkeyLabel())
        }
    }

    func currentHotkeyLabel() -> String {
        hotkeyLabel(keyCode: savedKeyCode(), modifiers: savedModifiers())
    }
}

// MARK: - Hotkey helpers

func savedKeyCode() -> UInt32 {
    let v = UserDefaults.standard.integer(forKey: kUDKeyCode)
    return v > 0 ? UInt32(v) : kDefaultKeyCode
}
func savedModifiers() -> UInt32 {
    let v = UserDefaults.standard.integer(forKey: kUDModifiers)
    return v > 0 ? UInt32(v) : kDefaultMods
}
func saveHotkey(keyCode: UInt32, modifiers: UInt32) {
    UserDefaults.standard.set(Int(keyCode),   forKey: kUDKeyCode)
    UserDefaults.standard.set(Int(modifiers), forKey: kUDModifiers)
}

func carbonModifiers(from f: NSEvent.ModifierFlags) -> UInt32 {
    var m: UInt32 = 0
    if f.contains(.command) { m |= UInt32(cmdKey) }
    if f.contains(.option)  { m |= UInt32(optionKey) }
    if f.contains(.shift)   { m |= UInt32(shiftKey) }
    if f.contains(.control) { m |= UInt32(controlKey) }
    return m
}

func hotkeyLabel(keyCode: UInt32, modifiers: UInt32) -> String {
    var s = ""
    if modifiers & UInt32(controlKey) != 0 { s += "⌃" }
    if modifiers & UInt32(optionKey)  != 0 { s += "⌥" }
    if modifiers & UInt32(shiftKey)   != 0 { s += "⇧" }
    if modifiers & UInt32(cmdKey)     != 0 { s += "⌘" }
    return s + keyCodeToString(keyCode)
}

func keyCodeToString(_ k: UInt32) -> String {
    let m: [UInt32:String] = [
        0:"A",1:"S",2:"D",3:"F",4:"H",5:"G",6:"Z",7:"X",8:"C",9:"V",
        11:"B",12:"Q",13:"W",14:"E",15:"R",16:"Y",17:"T",18:"1",19:"2",
        20:"3",21:"4",22:"6",23:"5",24:"=",25:"9",26:"7",27:"-",28:"8",
        29:"0",30:"]",31:"O",32:"U",33:"[",34:"I",35:"P",36:"↩",37:"L",
        38:"J",39:"'",40:"K",41:";",42:"\\",43:",",44:"/",45:"N",46:"M",
        47:".",48:"⇥",49:"Space",50:"`",51:"⌫",53:"⎋",
        96:"F5",97:"F6",98:"F7",99:"F3",100:"F8",101:"F9",103:"F11",
        122:"F1",123:"←",124:"→",125:"↓",126:"↑",
    ]
    return m[k] ?? "(\(k))"
}

// MARK: - HotkeyTextField

class HotkeyTextField: NSTextField {
    var capturedKeyCode:  UInt32 = 0
    var capturedModifiers: UInt32 = 0
    override var acceptsFirstResponder: Bool { true }

    // performKeyEquivalent fires before the field editor or menu system
    // can swallow Cmd/Ctrl combos. This is the only reliable intercept point.
    override func performKeyEquivalent(with e: NSEvent) -> Bool {
        guard currentEditor() != nil else { return super.performKeyEquivalent(with: e) }
        return captureEvent(e)
    }

    override func keyDown(with e: NSEvent) {
        if !captureEvent(e) {
            super.keyDown(with: e)
        }
    }

    @discardableResult
    private func captureEvent(_ e: NSEvent) -> Bool {
        // Ignore bare modifier presses (no actual key)
        let modOnlyKeyCodes: Set<UInt16> = [0x37,0x38,0x3A,0x3B,0x3C,0x36, // L/R cmd,shift,opt,ctrl
                                            0x3D,0x3E,0x3F]                  // R-opt, R-ctrl, fn
        guard !modOnlyKeyCodes.contains(e.keyCode) else { return false }
        // Need at least one modifier for a hotkey
        let mods = carbonModifiers(from: e.modifierFlags)
        guard mods != 0 else { return false }
        capturedKeyCode   = UInt32(e.keyCode)
        capturedModifiers = mods
        stringValue = hotkeyLabel(keyCode: capturedKeyCode, modifiers: capturedModifiers)
        return true
    }
}

// MARK: - Atmosphere layer (grid + scanlines + vignette)

class AtmosphereView: NSView {
    override var isFlipped: Bool { false }
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
    override func draw(_ dirtyRect: NSRect) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }

        let b = self.bounds

        // 1. Grid floor — 32-pt cyan grid, opacity 0.14, masked by radial gradient
        //    anchored at bottom-center so lines are strongest near the base.
        let gridColor = cCyan.withAlphaComponent(0.14)
        let gridSize: CGFloat = 32
        gridColor.setStroke()
        ctx.setLineWidth(0.5)

        // horizontal lines
        var y = fmod(b.minY, gridSize)
        while y <= b.maxY {
            ctx.move(to: CGPoint(x: b.minX, y: y))
            ctx.addLine(to: CGPoint(x: b.maxX, y: y))
            y += gridSize
        }
        // vertical lines
        var x = fmod(b.minX, gridSize)
        while x <= b.maxX {
            ctx.move(to: CGPoint(x: x, y: b.minY))
            ctx.addLine(to: CGPoint(x: x, y: b.maxY))
            x += gridSize
        }
        ctx.strokePath()

        // 2. Scanlines — thin horizontal stripes, blend overlay at low opacity
        let stripeH: CGFloat = 3
        var sy: CGFloat = b.minY
        while sy <= b.maxY {
            NSColor.white.withAlphaComponent(0.03).setFill()
            NSRect(x: b.minX, y: sy, width: b.width, height: stripeH * 0.5).fill()
            sy += stripeH
        }

        // 3. Vignette — radial gradient, transparent center → dark edge
        let center = CGPoint(x: b.midX, y: b.midY)
        let radius  = max(b.width, b.height) * 0.75
        let colors  = [NSColor.clear.cgColor, NSColor(white: 0, alpha: 0.30).cgColor]
        let locs: [CGFloat] = [0.45, 1.0]
        if let grad = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(),
                                 colors: colors as CFArray, locations: locs) {
            ctx.drawRadialGradient(grad,
                startCenter: center, startRadius: 0,
                endCenter:   center, endRadius:   radius,
                options: [.drawsAfterEndLocation])
        }
    }
}

// MARK: - Corner bracket view

class BracketView: NSView {
    var color: NSColor = cCyan
    var size: CGFloat  = 10
    var thick: CGFloat = 1.5
    var corners: Set<String> = ["tl","tr","bl","br"]

    override func hitTest(_ point: NSPoint) -> NSView? { nil }
    override func draw(_ dirtyRect: NSRect) {
        color.setStroke()
        let p = NSBezierPath()
        p.lineWidth = thick
        let b = bounds

        if corners.contains("tl") {
            p.move(to:    NSPoint(x: b.minX,        y: b.maxY - size))
            p.line(to:    NSPoint(x: b.minX,        y: b.maxY))
            p.line(to:    NSPoint(x: b.minX + size, y: b.maxY))
        }
        if corners.contains("tr") {
            p.move(to:    NSPoint(x: b.maxX - size, y: b.maxY))
            p.line(to:    NSPoint(x: b.maxX,        y: b.maxY))
            p.line(to:    NSPoint(x: b.maxX,        y: b.maxY - size))
        }
        if corners.contains("bl") {
            p.move(to:    NSPoint(x: b.minX,        y: b.minY + size))
            p.line(to:    NSPoint(x: b.minX,        y: b.minY))
            p.line(to:    NSPoint(x: b.minX + size, y: b.minY))
        }
        if corners.contains("br") {
            p.move(to:    NSPoint(x: b.maxX - size, y: b.minY))
            p.line(to:    NSPoint(x: b.maxX,        y: b.minY))
            p.line(to:    NSPoint(x: b.maxX,        y: b.minY + size))
        }
        p.stroke()
    }
}

// MARK: - Pulsing dot view

class PulsingDotView: NSView {
    private var alpha: CGFloat = 1.0
    private var timer: Timer?

    override func hitTest(_ point: NSPoint) -> NSView? { nil }
    override init(frame: NSRect) {
        super.init(frame: frame)
        start()
    }
    required init?(coder: NSCoder) { fatalError() }

    func start() {
        timer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in
            guard let self = self else { return }
            self.alpha = 0.5 + 0.5 * CGFloat(sin(Date().timeIntervalSinceReferenceDate * 2.5))
            self.needsDisplay = true
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        let r = NSBezierPath(ovalIn: bounds.insetBy(dx: 0.5, dy: 0.5))
        cMag.withAlphaComponent(alpha).setFill()
        r.fill()
    }

    deinit { timer?.invalidate() }
}

// MARK: - Live timestamp label

class TimestampLabel: NSTextField {
    private var timer: Timer?

    override init(frame: NSRect) {
        super.init(frame: frame)
        setup()
    }
    required init?(coder: NSCoder) { fatalError() }

    func setup() {
        isEditable = false; isBezeled = false; drawsBackground = false
        font = NSFont(name: "JetBrains Mono", size: 9.5)
            ?? NSFont.monospacedSystemFont(ofSize: 9.5, weight: .regular)
        textColor = cDim
        alignment = .right
        tick()
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            self?.tick()
        }
    }

    func tick() {
        let f = DateFormatter()
        f.dateFormat = "yyyy.MM.dd · HH:mm"
        stringValue = f.string(from: Date())
    }

    deinit { timer?.invalidate() }
}

// MARK: - Glow border box

class GlowBox: NSView {
    var borderColor: NSColor = cCyan
    var glowRadius: CGFloat  = 12

    override var isFlipped: Bool { false }
    override func hitTest(_ point: NSPoint) -> NSView? {
        // Let subviews (scrollView, textField) receive events; block hits on the box itself
        return super.hitTest(point)
    }
    override func draw(_ dirtyRect: NSRect) {
        let b = bounds.insetBy(dx: 1, dy: 1)
        let path = NSBezierPath(rect: b)
        path.lineWidth = 2.0

        // Glow shadow
        let ctx = NSGraphicsContext.current!.cgContext
        ctx.setShadow(offset: .zero, blur: glowRadius,
                      color: borderColor.withAlphaComponent(0.6).cgColor)
        borderColor.setStroke()
        path.stroke()

        ctx.setShadow(offset: .zero, blur: 0, color: nil)

        // Subtle inner fill
        NSColor(white: 0.08, alpha: 0.4).setFill()
        NSBezierPath(rect: bounds).fill()
    }
}

// MARK: - FragWindowController

class FragWindowController: NSObject, NSWindowDelegate {
    var window: NSWindow!
    var textView: FragTextView!
    var tagsField: TagsTextField!
    var statusLabel: NSTextField!
    var footHintLabel: NSTextField!
    var timestampLabel: TimestampLabel!
    var hotkeyLabelView: NSTextField!
    var onSetHotkey: (() -> Void)?
    var suppressHide = false

    private let W: CGFloat = 620
    private let H: CGFloat = 380

    override init() {
        super.init()
        buildWindow()
    }

    // MARK: Build

    func buildWindow() {
        window = FragWindow(
            contentRect: NSRect(x: 0, y: 0, width: W, height: H),
            styleMask: [.borderless, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.backgroundColor  = .clear
        window.isOpaque         = false
        window.hasShadow        = true
        window.center()
        window.delegate         = self
        window.isReleasedWhenClosed  = false
        window.level             = .floating
        window.isMovableByWindowBackground = true
        window.ignoresMouseEvents = false
        window.acceptsMouseMovedEvents = true

        let root = FragRootView(frame: NSRect(x: 0, y: 0, width: W, height: H))
        window.contentView = root

        buildContent(in: root)
    }

    func buildContent(in root: NSView) {
        // Atmosphere (behind everything)
        let atmo = AtmosphereView(frame: root.bounds)
        atmo.autoresizingMask = [.width, .height]
        root.addSubview(atmo)

        // Content inset: 40 top (traffic lights), 22 sides, 18 bottom
        let pad: CGFloat    = 22
        let topPad: CGFloat = 40
        let botPad: CGFloat = 18
        let innerW = W - pad * 2

        // ── Status header ──────────────────────────────────────────
        // y position: just below top padding
        let headerY = H - topPad - 20
        let headerH: CGFloat = 16

        // Pulsing dot
        let dot = PulsingDotView(frame: NSRect(x: pad, y: headerY + 4, width: 7, height: 7))
        root.addSubview(dot)

        // System label  "FRAG.ENGINE"
        let sysLabel = monoLabel("FRAG.ENGINE", size: 10.5, color: cCyan)
        sysLabel.frame = NSRect(x: pad + 14, y: headerY, width: 100, height: headerH)
        root.addSubview(sysLabel)

        // separator "//"
        let slashLabel = monoLabel("//", size: 10.5, color: cSep)
        slashLabel.frame = NSRect(x: pad + 118, y: headerY, width: 20, height: headerH)
        root.addSubview(slashLabel)

        // Route label  "NEW_FRAGMENT"
        let routeLabel = monoLabel("NEW_FRAGMENT", size: 10.5, color: cSec)
        routeLabel.frame = NSRect(x: pad + 142, y: headerY, width: 120, height: headerH)
        root.addSubview(routeLabel)

        // Timestamp (right-aligned, leaves room for keyboard button)
        let ts = TimestampLabel(frame: NSRect(x: W - pad - 160, y: headerY, width: 140, height: headerH))
        ts.autoresizingMask = [.minXMargin]
        root.addSubview(ts)
        timestampLabel = ts

        // Keyboard icon button — far right of header, added last so it's on top
        let hkBtn = NSButton(frame: NSRect(x: W - pad - 16, y: headerY + 3, width: 16, height: headerH))
        hkBtn.bezelStyle  = .regularSquare
        hkBtn.isBordered  = false
        hkBtn.image       = NSImage(systemSymbolName: "keyboard", accessibilityDescription: "Set Hotkey")
        hkBtn.image?.isTemplate = true
        hkBtn.contentTintColor  = cDim
        hkBtn.toolTip     = "Set global hotkey (\(hotkeyLabel(keyCode: savedKeyCode(), modifiers: savedModifiers())))"
        hkBtn.target      = self
        hkBtn.action      = #selector(hotkeyButtonClicked)
        hkBtn.autoresizingMask = [.minXMargin]
        root.addSubview(hkBtn)



        // ── Layout constants (bottom-up) ───────────────────────────
        // foot hint row
        let footY:      CGFloat = botPad               // 18
        let footH:      CGFloat = 14
        // tags row
        let tagsRowY:   CGFloat = footY + footH + 10   // 42
        let tagsRowH:   CGFloat = 32
        // tags label above the row
        let tagsLblY:   CGFloat = tagsRowY + tagsRowH + 5   // 79
        let tagsLblH:   CGFloat = 14
        // gap between tags label and textarea bottom
        let textAreaY:  CGFloat = tagsLblY + tagsLblH + 6   // 99
        // textarea top: below the status header
        let headerBottom: CGFloat = H - topPad - 20 - 8     // leaves 8pt gap
        let textAreaH:  CGFloat = headerBottom - textAreaY

        // ── Main textarea ──────────────────────────────────────────
        let textGlow = GlowBox(frame: NSRect(x: pad, y: textAreaY, width: innerW, height: textAreaH))
        textGlow.borderColor = cCyan
        textGlow.glowRadius  = 16
        textGlow.autoresizingMask = [.width, .height]
        root.addSubview(textGlow)

        let textBrack = BracketView(frame: textGlow.frame.insetBy(dx: -1, dy: -1))
        textBrack.color   = cCyan
        textBrack.size    = 10
        textBrack.thick   = 2.0
        textBrack.corners = ["tl","tr","bl","br"]
        textBrack.autoresizingMask = [.width, .height]
        root.addSubview(textBrack)

        let scrollView = NSScrollView(frame: textGlow.bounds.insetBy(dx: 1, dy: 1))
        scrollView.hasVerticalScroller   = false   // hidden per spec
        scrollView.hasHorizontalScroller = false
        scrollView.drawsBackground       = false
        scrollView.autoresizingMask      = [.width, .height]
        textGlow.addSubview(scrollView)

        let tv = FragTextView(frame: scrollView.bounds)
        tv.isEditable  = true
        tv.isRichText  = false
        tv.font        = NSFont(name: "Space Grotesk", size: 14.5)
            ?? NSFont.systemFont(ofSize: 14.5)
        tv.textColor   = NSColor(srgbRed: 0.75, green: 0.94, blue: 0.98, alpha: 1) // cyan-white tint from border
        tv.textContainerInset = NSSize(width: 10, height: 12)
        tv.drawsBackground    = false
        tv.isAutomaticQuoteSubstitutionEnabled  = false
        tv.isAutomaticDashSubstitutionEnabled   = false
        tv.isAutomaticSpellingCorrectionEnabled = false
        tv.isGrammarCheckingEnabled             = false
        tv.autoresizingMask = [.width, .height]
        tv.delegate = self
        tv.string   = ""
        scrollView.documentView = tv
        textView = tv
        tv.insertionPointColor = cCyan
        tv.onSubmit = { [weak self] in self?.submit() }
        tv.onEscape = { [weak self] in self?.clearAndHide() }
        tv.onTab    = { [weak self] in self?.focusTags() }

        // ── Tags label  "▸ tags"  — above the row ─────────────────
        let tagsHdrLbl = monoLabel("▸  tags", size: 9.5, color: cMag)
        tagsHdrLbl.frame = NSRect(x: pad, y: tagsLblY, width: 80, height: tagsLblH)
        root.addSubview(tagsHdrLbl)

        // ── Tags row ───────────────────────────────────────────────
        let tagsGlow = GlowBox(frame: NSRect(x: pad, y: tagsRowY, width: innerW, height: tagsRowH))
        tagsGlow.borderColor = cMag
        tagsGlow.glowRadius  = 10
        tagsGlow.autoresizingMask = [.width]
        root.addSubview(tagsGlow)

        let tagsBrack = BracketView(frame: tagsGlow.frame.insetBy(dx: -1, dy: -1))
        tagsBrack.color   = cMag
        tagsBrack.size    = 6
        tagsBrack.thick   = 1.5
        tagsBrack.corners = ["tl","tr","bl","br"]
        tagsBrack.autoresizingMask = [.width]
        root.addSubview(tagsBrack)

        let tf = TagsTextField(frame: NSRect(x: 12, y: 6, width: tagsGlow.bounds.width - 24, height: 20))
        tf.placeholderString = ""
        tf.font              = NSFont(name: "JetBrains Mono", size: 13)
            ?? NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
        tf.textColor         = NSColor(srgbRed: 0.957, green: 0.824, blue: 0.918, alpha: 1) // #f4d2ea light pink
        tf.isBordered        = false
        tf.drawsBackground   = false
        tf.focusRingType     = .none
        tf.autoresizingMask  = [.width]
        tagsGlow.addSubview(tf)
        tagsField = tf
        tf.delegate = self
        tf.onTab = { [weak self] in self?.focusText() }

        // ── Status / feedback label ────────────────────────────────
        let sl = NSTextField(labelWithString: "")
        sl.frame = NSRect(x: pad, y: botPad - 2, width: innerW - 90, height: 14)
        sl.font  = NSFont(name: "JetBrains Mono", size: 9.5)
            ?? NSFont.monospacedSystemFont(ofSize: 9.5, weight: .regular)
        sl.textColor = cSec
        sl.autoresizingMask = [.width, .minYMargin]
        root.addSubview(sl)
        statusLabel = sl

        // ── Foot hint  "⌘ ↵  COMMIT" ─────────────────────────────
        let fh = monoLabel("⌘ ↵  commit", size: 9.5, color: cDim)
        fh.alignment = .right
        fh.frame = NSRect(x: W - pad - 100, y: botPad - 2, width: 100, height: 14)
        fh.autoresizingMask = [.minXMargin]
        root.addSubview(fh)
        footHintLabel = fh

    }

    // MARK: Show / Hide

    func showWindow() {
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        window.makeFirstResponder(textView)
        updateHotkeyLabel(hotkeyLabel(keyCode: savedKeyCode(), modifiers: savedModifiers()))
    }

    func hideWindow() {
        window.orderOut(nil)
    }

    func focusTags() {
        window.makeFirstResponder(tagsField)
    }

    func focusText() {
        window.makeFirstResponder(textView)
    }

    func updateHotkeyLabel(_ label: String) {
        if let btn = window.contentView?.subviews.first(where: {
            ($0 as? NSButton)?.image?.accessibilityDescription == "Set Hotkey"
        }) as? NSButton {
            btn.toolTip = "Set global hotkey (\(label))"
        }
    }

    @objc func hotkeyButtonClicked() {
        onSetHotkey?()
    }

    // MARK: Submit

    func submit() {
        let content = textView.string.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !content.isEmpty else {
            flash("content cannot be empty", isError: true)
            return
        }

        let rawTags = tagsField.stringValue
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }

        statusLabel.textColor   = cSec
        statusLabel.stringValue = "transmitting…"

        submitToAPI(content: content, tags: rawTags) { [weak self] result in
            DispatchQueue.main.async {
                guard let self = self else { return }
                switch result {
                case .success(let msg):
                    self.flash(msg, isError: false)
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                        self.clearAndHide()
                    }
                case .failure(let err):
                    self.flash(err.localizedDescription, isError: true)
                }
            }
        }
    }

    func clearAndHide() {
        textView.string         = ""
        tagsField.stringValue   = ""
        statusLabel.stringValue = ""
        hideWindow()
    }

    func flash(_ msg: String, isError: Bool) {
        statusLabel.textColor   = isError ? NSColor.systemRed : cCyan
        statusLabel.stringValue = msg
        guard isError else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in
            self?.statusLabel.textColor   = cSec
            self?.statusLabel.stringValue = ""
        }
    }

    // MARK: Actions



    // MARK: Delegate

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        hideWindow(); return false
    }

    func windowDidResignKey(_ notification: Notification) {
        guard !suppressHide else { return }
        hideWindow()
    }

    // MARK: Helpers

    func monoLabel(_ text: String, size: CGFloat, color: NSColor) -> NSTextField {
        let lbl = NSTextField(labelWithString: text)
        lbl.isEditable = false; lbl.isBezeled = false; lbl.drawsBackground = false
        lbl.font = NSFont(name: "JetBrains Mono", size: size)
            ?? NSFont.monospacedSystemFont(ofSize: size, weight: .regular)
        lbl.textColor = color
        lbl.textColor?.withAlphaComponent(1)
        return lbl
    }
}

// MARK: - NSTextViewDelegate

extension FragWindowController: NSTextViewDelegate, NSTextFieldDelegate {
    func textDidChange(_ notification: Notification) {
        flashTextView()
    }

    func controlTextDidChange(_ notification: Notification) {
        flashTagsField()
    }
}

// MARK: - Keystroke flash

private let kTextNormal = NSColor(srgbRed: 0.75, green: 0.94, blue: 0.98, alpha: 1)
private let kTextFlash  = NSColor(srgbRed: 0.92, green: 1.00, blue: 1.00, alpha: 1)
private let kTagsNormal = NSColor(srgbRed: 0.957, green: 0.824, blue: 0.918, alpha: 1)
private let kTagsFlash  = NSColor(srgbRed: 1.00,  green: 0.94,  blue: 0.98,  alpha: 1)

extension FragWindowController {
    func flashTextView() {
        textView.textColor = kTextFlash
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.09) { [weak self] in
            self?.textView.textColor = kTextNormal
        }
    }

    func flashTagsField() {
        if let editor = tagsField.currentEditor() as? NSTextView {
            editor.textColor = kTagsFlash
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.09) { [weak self] in
                _ = self
                editor.textColor = kTagsNormal
            }
        }
    }
}

// MARK: - Window subclass

class FragWindow: NSWindow {
    override var canBecomeKey: Bool  { true }
    override var canBecomeMain: Bool { true }

    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 53: // Escape
            (delegate as? FragWindowController)?.clearAndHide()
        case 36 where event.modifierFlags.contains(.command): // Cmd+Enter
            (delegate as? FragWindowController)?.submit()
        default:
            super.keyDown(with: event)
        }
    }
}

// MARK: - Root view (rounded dark background)

class FragRootView: NSView {
    override var isFlipped: Bool { false }
    // Allow dragging from any part of the root not covered by an interactive subview
    override var mouseDownCanMoveWindow: Bool { true }
    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(roundedRect: bounds, xRadius: 12, yRadius: 12)
        cBg.setFill()
        path.fill()
    }
}





// MARK: - FragTextView (intercepts Esc and Cmd+Enter before NSTextView swallows them)

class FragTextView: NSTextView {
    var onSubmit: (() -> Void)?
    var onEscape: (() -> Void)?
    var onTab:    (() -> Void)?

    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 36 where event.modifierFlags.contains(.command): // Cmd+Enter
            onSubmit?()
        case 53: // Escape
            onEscape?()
        case 48: // Tab
            onTab?()
        default:
            super.keyDown(with: event)
        }
    }
}

// MARK: - Tags text field (tinted, Tab cycles back to textarea)

class TagsTextField: NSTextField {
    var onTab: (() -> Void)?

    override func becomeFirstResponder() -> Bool {
        let r = super.becomeFirstResponder()
        if let editor = currentEditor() as? NSTextView {
            editor.insertionPointColor = cMag
        }
        return r
    }

    override func keyDown(with event: NSEvent) {
        if event.keyCode == 48 { // Tab
            onTab?()
            return
        }
        super.keyDown(with: event)
    }
}

// MARK: - API

struct IntakePayload: Codable { let content: String; let tags: [String] }
struct IntakeResponse: Codable {
    struct Result: Codable { let fragment_id, outcome, status: String }
    let result: Result
}

func submitToAPI(content: String, tags: [String],
                 completion: @escaping (Result<String, Error>) -> Void) {
    guard let url = URL(string: kIntakeEndpoint) else {
        completion(.failure(NSError(domain:"frag", code:1,
            userInfo:[NSLocalizedDescriptionKey:"Bad endpoint URL"]))); return
    }
    var req = URLRequest(url: url, timeoutInterval: 10)
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    guard let body = try? JSONEncoder().encode(IntakePayload(content: content, tags: tags)) else {
        completion(.failure(NSError(domain:"frag", code:2,
            userInfo:[NSLocalizedDescriptionKey:"Encode error"]))); return
    }
    req.httpBody = body
    URLSession.shared.dataTask(with: req) { data, resp, err in
        if let err = err { completion(.failure(err)); return }
        guard let h = resp as? HTTPURLResponse else {
            completion(.failure(NSError(domain:"frag", code:3,
                userInfo:[NSLocalizedDescriptionKey:"No response"]))); return
        }
        guard (200...299).contains(h.statusCode) else {
            let b = data.flatMap { String(data:$0, encoding:.utf8) } ?? "err"
            completion(.failure(NSError(domain:"frag", code:h.statusCode,
                userInfo:[NSLocalizedDescriptionKey:"HTTP \(h.statusCode): \(b)"]))); return
        }
        guard let data,
              let decoded = try? JSONDecoder().decode(IntakeResponse.self, from: data)
        else { completion(.success("sent")); return }
        let r = decoded.result
        let s = String(r.fragment_id.prefix(8))
        completion(.success(r.outcome == "skipped"
            ? "already exists (\(s)…)"
            : "→ \(r.status)  [\(s)…]"))
    }.resume()
}

// MARK: - Entry point

let app = NSApplication.shared
let appDelegate = AppDelegate()
gAppDelegate  = appDelegate
app.delegate  = appDelegate
app.run()
