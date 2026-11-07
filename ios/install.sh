#!/bin/bash
# Builds the iPhone app, signs it with your Apple ID and installs it on the
# iPhone connected to this Mac.
#
#   ./ios/install.sh
#
# One-time setup: install Xcode, open it once, and add your Apple ID under
# Xcode › Settings › Accounts. Overrides: TEAM=XXXXXXXXXX (Apple team ID),
# BUNDLE_ID=com.example.router, DEVICE=<iPhone name or UDID>.
set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\n\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
step() { printf '\033[1m==> %s\033[0m\n' "$*"; }

# --- Xcode ---------------------------------------------------------------
if ! xcodebuild -version >/dev/null 2>&1; then
	die "the full Xcode app is needed (the Command Line Tools alone can't build for iPhone).
Install Xcode from the App Store, open it once, then run:
  sudo xcode-select -s /Applications/Xcode.app"
fi
xcrun devicectl --version >/dev/null 2>&1 || die "this needs Xcode 15 or newer (for devicectl)."
step "Using $(xcodebuild -version | head -1)"

# --- Apple team -----------------------------------------------------------
if [[ -z "${TEAM:-}" ]]; then
	TEAM=$(defaults read com.apple.dt.Xcode 2>/dev/null |
		grep -oE 'teamID = "?[A-Z0-9]{10}' | head -1 | grep -oE '[A-Z0-9]{10}$' || true)
fi
[[ -n "${TEAM:-}" ]] || die "no Apple ID found in Xcode.
Open Xcode › Settings › Accounts, click + and add your Apple ID, then run this again.
(Or pass your team ID: TEAM=XXXXXXXXXX ./ios/install.sh)"
BUNDLE_ID="${BUNDLE_ID:-com.$(echo "$TEAM" | tr 'A-Z' 'a-z').router}"
step "Signing with team $TEAM as $BUNDLE_ID"

# --- iPhone ---------------------------------------------------------------
JSON=$(mktemp)
trap 'rm -f "$JSON"' EXIT
xcrun devicectl list devices --json-output "$JSON" >/dev/null 2>&1 || true
FOUND=$(/usr/bin/python3 - "$JSON" "${DEVICE:-}" <<'PY' || true
import json, sys
try:
    devices = json.load(open(sys.argv[1]))["result"]["devices"]
except Exception:
    devices = []
want = sys.argv[2].lower()
for d in devices:
    hw, props = d.get("hardwareProperties", {}), d.get("deviceProperties", {})
    if hw.get("platform") != "iOS" or hw.get("reality") == "virtual":
        continue
    name, udid = props.get("name", ""), hw.get("udid", "")
    if want and want not in (name.lower(), udid.lower()):
        continue
    if udid:
        print(udid, name)
        break
PY
)
UDID=${FOUND%% *}
NAME=${FOUND#* }
[[ -n "${UDID:-}" ]] || die "no iPhone found.
Plug your iPhone into this Mac with a cable, unlock it and tap Trust, then run this again.
If it's connected and still not found, open Xcode › Window › Devices and Simulators once."
step "Installing on \"$NAME\" ($UDID)"

# --- Build and sign -------------------------------------------------------
step "Building (the first build takes a few minutes)"
LOG=build/install.log
mkdir -p build
if ! xcodebuild -project Router.xcodeproj -scheme Router -configuration Release \
	-destination "platform=iOS,id=$UDID" -derivedDataPath build/device \
	-allowProvisioningUpdates -allowProvisioningDeviceRegistration \
	DEVELOPMENT_TEAM="$TEAM" PRODUCT_BUNDLE_IDENTIFIER="$BUNDLE_ID" CODE_SIGN_STYLE=Automatic \
	build >"$LOG" 2>&1; then
	grep -E "error:" "$LOG" | sort -u | head -15 >&2 || true
	if grep -q "Developer Mode" "$LOG"; then
		die "turn on Developer Mode on the iPhone: Settings › Privacy & Security › Developer Mode (the phone restarts), then run this again."
	fi
	die "the build failed. Full log: ios/$LOG"
fi
APP=build/device/Build/Products/Release-iphoneos/Router.app
[[ -d "$APP" ]] || die "build finished but $APP is missing. Full log: ios/$LOG"

# --- Install and launch ---------------------------------------------------
step "Installing"
xcrun devicectl device install app --device "$UDID" "$APP" >>"$LOG" 2>&1 ||
	die "install failed. If the iPhone asks, turn on Developer Mode (Settings › Privacy & Security › Developer Mode). Full log: ios/$LOG"

step "Launching"
if ! xcrun devicectl device process launch --device "$UDID" "$BUNDLE_ID" >>"$LOG" 2>&1; then
	cat <<MSG

Installed, but iOS won't open it until you trust your developer account:
  on the iPhone, open Settings › General › VPN & Device Management,
  tap your Apple ID, then Trust. After that, open Router from the Home Screen.
MSG
	exit 0
fi
printf '\n\033[32mDone.\033[0m Router is open on your iPhone.\n'
echo "With a free Apple ID it keeps working for 7 days; run this script again to renew."
