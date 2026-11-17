package app

import (
	"fmt"
	"testing"
)

// Matches the observed client layout cardinality and key sizes without reading
// a user's drafts, device catalog, or conversation history.
func BenchmarkEncodeStateLargeLayout(b *testing.B) {
	m := NewModel()
	for i := range 26000 {
		m.Collapsed[fmt.Sprintf("command-output:%q:%q:%q:%q", "299c2aeb-5056-4faf-8854-4c8bf33337c8", "1167fa83-0d9c-451a-af1e-24a34837a2ad", "", fmt.Sprintf("command-%08d-12345678-1234-1234-1234-123456789012", i))] = true
	}
	for _, changing := range []bool{false, true} {
		b.Run(fmt.Sprintf("draftChanges=%v", changing), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if changing {
					m.Drafts["home"] = fmt.Sprint(i)
				}
				if _, err := m.EncodeState(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
