package main

import (
	"os"
	"syscall"
	"time"
)

func deskClipSealTime(info os.FileInfo) (time.Time, bool) {
	stamp := info.ModTime()
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		birth := time.Unix(stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec)
		// Sealed native clips were created and written together. An edited or
		// recreated file with retained old mtime is not a trusted recovery anchor.
		if stat.Birthtimespec.Sec == 0 || stamp.Sub(birth) > 2*time.Second || birth.Sub(stamp) > 2*time.Second {
			return time.Time{}, false
		}
	}
	return stamp, true
}
