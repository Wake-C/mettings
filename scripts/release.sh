#!/bin/zsh
set -euo pipefail
cd "${0:A:h:h}"
: "${METTINGS_SIGN_IDENTITY:?Set METTINGS_SIGN_IDENTITY to your persistent Keychain code-signing identity}"
[[ "$METTINGS_SIGN_IDENTITY" != '-' ]] || { print -u2 'Release requires a persistent certificate, not ad-hoc signing'; exit 1; }
./scripts/build.sh
VERSION=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' build/Mettings.app/Contents/Info.plist)
NAME="Mettings-${VERSION}-macos-arm64-self-signed"
STAGE=$(mktemp -d "$PWD/build/.release.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
ditto build/Mettings.app "$STAGE/Mettings.app"
ln -s /Applications "$STAGE/Applications"
hdiutil create -volname Mettings -srcfolder "$STAGE" -ov -format UDZO "build/${NAME}.dmg"
(cd build && shasum -a 256 "${NAME}.dmg" > "${NAME}.dmg.sha256")
print "Release: $PWD/build/${NAME}.dmg"
