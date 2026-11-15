package permissionflow

import (
	"os"
	"path/filepath"
)

// FullDiskAccessGranted checks this process's effective access, never another
// bundle's permission. Open and immediately close FDA-protected databases;
// neither their contents nor the user's permission records are read or changed.
func FullDiskAccessGranted() bool {
	home, _ := os.UserHomeDir()
	return fullDiskAccessGranted(home, func(path string) bool {
		file, err := os.Open(path)
		if err != nil {
			return false
		}
		file.Close()
		return true
	})
}

func fullDiskAccessGranted(home string, canOpen func(string) bool) bool {
	if canOpen("/Library/Application Support/com.apple.TCC/TCC.db") {
		return true
	}
	return home != "" && canOpen(filepath.Join(home, "Library", "Application Support", "com.apple.TCC", "TCC.db"))
}
