//go:build !darwin

package main

import (
	"os"
	"time"
)

// Native recording is macOS-only. Other platforms cannot attest original clip
// birth times and should preserve the stored timestamp instead of estimating.
func deskClipSealTime(info os.FileInfo) (time.Time, bool) { return time.Time{}, false }
