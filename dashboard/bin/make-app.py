#!/usr/bin/env python3
"""Builds ~/Applications/Focusflow.app — a clickable launcher for the dashboard.

Clicking it makes sure the dashboard server (launchd job local.focusflow.dashboard)
is running, then opens the dashboard URL (with its access token) in the default
browser. Safe to re-run; it rebuilds the bundle in place.

    npm run app            # ~/Applications/Focusflow.app
    npm run app -- /Applications   # or another directory
"""
import os, shutil, subprocess, sys, tempfile
from PIL import Image, ImageDraw

NAME = "Focusflow"
EXECUTABLE = "Focusflow"  # native binary; "launcher" is the shell fallback
BUNDLE_ID = "local.focusflow.dashboard.dashboard"
dest_dir = os.path.expanduser(sys.argv[1] if len(sys.argv) > 1 else "~/Applications")
app = os.path.join(dest_dir, NAME + ".app")

LAUNCHER = r'''#!/bin/bash
# Opens the Focusflow dashboard, starting its server first if needed.
HOME_DIR="$HOME/.focusflow/dashboard"
LABEL="local.focusflow.dashboard"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
PORT=$(/usr/bin/python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.focusflow/dashboard/instance.json')))['tunables']['port'])" 2>/dev/null || echo 4177)

up() { /usr/bin/nc -z 127.0.0.1 "$PORT" >/dev/null 2>&1; }

if ! up; then
  if /bin/launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
    /bin/launchctl kickstart -k "gui/$(id -u)/$LABEL" >/dev/null 2>&1
  elif [ -f "$PLIST" ]; then
    /bin/launchctl bootstrap "gui/$(id -u)" "$PLIST" >/dev/null 2>&1
  fi
  for _ in $(seq 1 40); do up && break; sleep 0.5; done
fi

if ! up; then
  /usr/bin/osascript -e 'display alert "Focusflow dashboard is not running" message "The focusflow server did not start. Run: cd ~/Documents/random/focusflow && npm start" as critical' >/dev/null 2>&1
  exit 1
fi

TOKEN=$(cat "$HOME_DIR/token" 2>/dev/null)
/usr/bin/open "http://127.0.0.1:$PORT/#token=$TOKEN"
'''

def INFO_TEMPLATE(EXECUTABLE):
    return f'''<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>{NAME}</string>
  <key>CFBundleDisplayName</key><string>{NAME}</string>
  <key>CFBundleIdentifier</key><string>{BUNDLE_ID}</string>
  <key>CFBundleExecutable</key><string>{EXECUTABLE}</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
'''

def draw_icon(size=1024):
    S = size
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    # vertical gradient background clipped to a macOS-style squircle-ish rounded square
    grad = Image.new("RGBA", (S, S))
    top, bot = (99, 102, 241), (30, 27, 75)
    px = grad.load()
    for y in range(S):
        t = y / (S - 1)
        c = tuple(int(top[i] + (bot[i] - top[i]) * t) for i in range(3)) + (255,)
        for x in range(S):
            px[x, y] = c
    mask = Image.new("L", (S, S), 0)
    m = int(S * 0.055)
    ImageDraw.Draw(mask).rounded_rectangle((m, m, S - m, S - m), radius=int(S * 0.225), fill=255)
    img.paste(grad, (0, 0), mask)
    d = ImageDraw.Draw(img)
    # ring
    cx = cy = S // 2
    R, W = int(S * 0.26), int(S * 0.055)
    d.ellipse((cx - R, cy - R, cx + R, cy + R), outline=(255, 255, 255, 235), width=W)
    # check mark
    pts = [(cx - R * 0.42, cy + R * 0.04), (cx - R * 0.09, cy + R * 0.36), (cx + R * 0.44, cy - R * 0.30)]
    d.line(pts, fill=(255, 255, 255, 255), width=int(W * 1.1), joint="curve")
    for p in (pts[0], pts[-1]):
        r = int(W * 0.55)
        d.ellipse((p[0] - r, p[1] - r, p[0] + r, p[1] + r), fill=(255, 255, 255, 255))
    return img

def build_icns(path):
    base = draw_icon(1024)
    with tempfile.TemporaryDirectory() as td:
        iconset = os.path.join(td, "AppIcon.iconset")
        os.makedirs(iconset)
        for s in (16, 32, 128, 256, 512):
            base.resize((s, s), Image.LANCZOS).save(os.path.join(iconset, f"icon_{s}x{s}.png"))
            base.resize((s * 2, s * 2), Image.LANCZOS).save(os.path.join(iconset, f"icon_{s}x{s}@2x.png"))
        subprocess.check_call(["iconutil", "-c", "icns", iconset, "-o", path])

SWIFT_SRC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "mac", "Focusflow.swift")

def build_native(dest):
    """Compile the Swift app (window + always-on-top mini board). Returns True on success."""
    if not os.path.exists(SWIFT_SRC) or shutil.which("swiftc") is None:
        return False
    r = subprocess.run(["swiftc", "-O", "-framework", "Cocoa", "-framework", "WebKit", SWIFT_SRC, "-o", dest],
                       capture_output=True, text=True)
    if r.returncode != 0:
        print("swiftc failed; falling back to the shell launcher:\n" + r.stderr[-800:])
        return False
    return True

def main():
    if os.path.exists(app):
        shutil.rmtree(app)
    macos = os.path.join(app, "Contents", "MacOS")
    res = os.path.join(app, "Contents", "Resources")
    os.makedirs(macos)
    os.makedirs(res)
    native = build_native(os.path.join(macos, "Focusflow"))
    exe = "Focusflow" if native else "launcher"
    with open(os.path.join(app, "Contents", "Info.plist"), "w") as f:
        f.write(INFO_TEMPLATE(exe))
    if not native:
        launcher = os.path.join(macos, "launcher")
        with open(launcher, "w") as f:
            f.write(LAUNCHER)
        os.chmod(launcher, 0o755)
    build_icns(os.path.join(res, "AppIcon.icns"))
    subprocess.call(["codesign", "--force", "--deep", "-s", "-", app], stderr=subprocess.DEVNULL)
    subprocess.call(["touch", app])
    print(f"built {app}")

main()
