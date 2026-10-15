#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="Frag"
BUNDLE_ID="com.hollis-labs.frag"
INSTALL_DIR="$HOME/Applications"
APP_BUNDLE="$INSTALL_DIR/$APP_NAME.app"

echo "==> Building $APP_NAME.app"

# --- Compile ---
BINARY_OUT="$SCRIPT_DIR/$APP_NAME"
swiftc \
  -O \
  -target arm64-apple-macos13.0 \
  -framework AppKit \
  -framework Foundation \
  -framework Carbon \
  "$SCRIPT_DIR/main.swift" \
  -o "$BINARY_OUT"

echo "    Compiled: $BINARY_OUT"

# --- Assemble bundle ---
rm -rf "$APP_BUNDLE"
mkdir -p "$APP_BUNDLE/Contents/MacOS"
mkdir -p "$APP_BUNDLE/Contents/Resources"

cp "$BINARY_OUT" "$APP_BUNDLE/Contents/MacOS/$APP_NAME"
cp "$SCRIPT_DIR/Info.plist" "$APP_BUNDLE/Contents/Info.plist"

# Generate a simple square.and.pencil icon via sips/iconutil if possible,
# otherwise skip (macOS will use a default icon)
if command -v python3 &>/dev/null; then
  python3 - "$APP_BUNDLE/Contents/Resources" <<'PYEOF'
import subprocess, sys, os, tempfile

resources_dir = sys.argv[1]

# Use SF Symbol rendering via osascript/Quartz — simplest approach:
# create a tiny NSImage-based PNG by running a Swift one-liner
swift_code = '''
import AppKit
import Foundation

let img = NSImage(systemSymbolName: "square.and.pencil", accessibilityDescription: nil)!
let size = NSSize(width: 512, height: 512)
let rep = NSBitmapImageRep(bitmapDataPlanes: nil,
                            pixelsWide: Int(size.width), pixelsHigh: Int(size.height),
                            bitsPerSample: 8, samplesPerPixel: 4,
                            hasAlpha: true, isPlanar: false,
                            colorSpaceName: .deviceRGB,
                            bytesPerRow: 0, bitsPerPixel: 0)!
let ctx = NSGraphicsContext(bitmapImageRep: rep)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = ctx
NSColor.black.setFill()
NSBezierPath(rect: NSRect(origin: .zero, size: size)).fill()
img.draw(in: NSRect(origin: .zero, size: size))
NSGraphicsContext.restoreGraphicsState()
let data = rep.representation(using: .png, properties: [:])!
let path = CommandLine.arguments[1]
try! data.write(to: URL(fileURLWithPath: path))
'''

tmpdir = tempfile.mkdtemp()
swift_file = os.path.join(tmpdir, "icon_gen.swift")
with open(swift_file, "w") as f:
    f.write(swift_code)

png_path = os.path.join(tmpdir, "icon_512.png")
result = subprocess.run(
    ["swift", swift_file, png_path],
    capture_output=True, timeout=30
)
if result.returncode != 0 or not os.path.exists(png_path):
    sys.exit(0)  # skip icon, not fatal

iconset = os.path.join(tmpdir, "AppIcon.iconset")
os.makedirs(iconset)

sizes = [16, 32, 64, 128, 256, 512]
for s in sizes:
    subprocess.run(
        ["sips", "-z", str(s), str(s), png_path, "--out", f"{iconset}/icon_{s}x{s}.png"],
        capture_output=True
    )
    subprocess.run(
        ["sips", "-z", str(s*2), str(s*2), png_path, "--out", f"{iconset}/icon_{s}x{s}@2x.png"],
        capture_output=True
    )

icns_path = os.path.join(resources_dir, "AppIcon.icns")
subprocess.run(["iconutil", "-c", "icns", iconset, "-o", icns_path], check=True)
print(f"    Icon generated: {icns_path}")
PYEOF
fi

# Clean up temp binary
rm -f "$BINARY_OUT"

# Remove quarantine so it opens without Gatekeeper blocking
xattr -dr com.apple.quarantine "$APP_BUNDLE" 2>/dev/null || true

echo "==> Done: $APP_BUNDLE"
echo ""
echo "To launch:"
echo "  open \"$APP_BUNDLE\""
echo ""
echo "To set a global hotkey (System Settings path):"
echo "  System Settings → Keyboard → Keyboard Shortcuts → App Shortcuts"
echo "  Click +, choose Frag.app, set Menu Title to 'Show Frag', assign your key combo"
echo ""
echo "To auto-launch at login:"
echo "  System Settings → General → Login Items → add $APP_BUNDLE"
