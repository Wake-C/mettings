#!/bin/zsh
set -euo pipefail
cd "${0:A:h:h}"
"${METTINGS_GO:-go}" test -race ./...
node --check frontend/app.js

node --check frontend/preferences.js
node --test frontend/state.test.cjs frontend/preferences-state.test.cjs
