package permissionflow

import (
	"slices"
	"testing"
)

func TestFullDiskAccessRequiresOpeningProtectedDatabase(t *testing.T) {
	const system = "/Library/Application Support/com.apple.TCC/TCC.db"
	const user = "/Users/test/Library/Application Support/com.apple.TCC/TCC.db"
	for _, readable := range []string{"", system, user} {
		t.Run(readable, func(t *testing.T) {
			var opened []string
			granted := fullDiskAccessGranted("/Users/test", func(path string) bool {
				opened = append(opened, path)
				return path == readable
			})
			if granted != (readable != "") {
				t.Fatal("grant does not match actual protected-file access", granted)
			}
			want := []string{system, user}
			if readable == system {
				want = want[:1]
			}
			if !slices.Equal(opened, want) {
				t.Fatal("probed unprotected or unrelated files", opened)
			}
		})
	}
}
