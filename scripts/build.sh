#!/bin/zsh
set -euo pipefail
cd "${0:A:h:h}"
METTINGS_GO=${METTINGS_GO:-go}
SIGN_IDENTITY=${METTINGS_SIGN_IDENTITY:--}
SIGN_ARGS=()
if [[ -n "${METTINGS_SIGN_KEYCHAIN:-}" ]]; then
 SIGN_ARGS=(--keychain "$METTINGS_SIGN_KEYCHAIN")
fi
xcrun swift scripts/render-tray.swift assets/tray.png
DEPS="$PWD/build/deps"
SDK="$DEPS/sherpa-onnx-v2.0.1-osx-arm64-shared-lib"
mkdir -p "$DEPS"
if [[ ! -f "$SDK/lib/libsherpa-onnx-c-api.dylib" ]]; then
 curl -fL --retry 3 'https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.1/sherpa-onnx-v2.0.1-osx-arm64-shared-lib.tar.bz2' -o "$DEPS/sherpa.tar.bz2"
 print 'e2d57ad9c83803d6c48f259caa66e44879bd4c4724c61f0d51a277b411bffb1d  '"$DEPS/sherpa.tar.bz2" | shasum -a 256 -c -
 tar -xjf "$DEPS/sherpa.tar.bz2" -C "$DEPS"
fi
STAGE=$(mktemp -d "$PWD/build/.package.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
APP="$STAGE/Mettings.app"
ENGINE="$APP/Contents/Resources/engine"
mkdir -p "$APP/Contents/MacOS" "$ENGINE/lib"
cp "$SDK/lib/libsherpa-onnx-c-api.dylib" "$SDK/lib/libonnxruntime.dylib" "$ENGINE/lib/"
xcrun clang++ -std=c++17 -O2 -arch arm64 -mmacosx-version-min=15.0 native/speech.cpp -L "$ENGINE/lib" -lsherpa-onnx-c-api -Wl,-rpath,@executable_path/lib -o "$ENGINE/mettings-speech"
xcrun clang++ -std=c++17 -O2 -arch arm64 -mmacosx-version-min=15.0 native/desk.cpp -L "$ENGINE/lib" -lsherpa-onnx-c-api -Wl,-rpath,@executable_path/lib -o "$ENGINE/mettings-desk"
CGO_ENABLED=0 GOARCH=arm64 "$METTINGS_GO" build -trimpath -o "$APP/Contents/MacOS/mettings" .
cp packaging/Info.plist "$APP/Contents/Info.plist"
cp NOTICE "$APP/Contents/Resources/NOTICE"
cp LICENSE "$APP/Contents/Resources/LICENSE"
cp native/include/*LICENSE "$APP/Contents/Resources/"
MYGO_DIR=$("$METTINGS_GO" list -m -f '{{.Dir}}' github.com/egoist/mygo)
cp "$MYGO_DIR/LICENSE" "$APP/Contents/Resources/MYGO-LICENSE"
ICONSET="$STAGE/Mettings.iconset"
mkdir -p "$ICONSET"
for size in 16 32 128 256 512; do
 sips -z "$size" "$size" assets/icon.png --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
 retina=$((size*2))
 sips -z "$retina" "$retina" assets/icon.png --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil --convert icns "$ICONSET" --output "$APP/Contents/Resources/Mettings.icns"
for binary in "$ENGINE/lib/"*.dylib "$ENGINE/mettings-speech" "$ENGINE/mettings-desk"; do codesign --force --sign "$SIGN_IDENTITY" "${SIGN_ARGS[@]}" "$binary"; done
codesign --force --sign "$SIGN_IDENTITY" "${SIGN_ARGS[@]}" --identifier com.mettings.desktop "$APP"
codesign --verify --deep --strict "$APP"
if [[ -d build/Mettings.app ]]; then mv build/Mettings.app "$STAGE/previous.app"; fi
mv "$APP" build/Mettings.app
print "Built: $PWD/build/Mettings.app"
