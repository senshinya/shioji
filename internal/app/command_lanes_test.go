package app

import (
	"testing"
	"time"
)

func TestNativeRuntimeChecksAllowTheWorkerCatalogDeadline(t *testing.T) {
	for _, method := range []string{"runtime.check", "runtime.configure", "runtime.agents"} {
		if got := commandTimeout(method); got <= 20*time.Second {
			t.Fatalf("%s has only %s to receive a 20-second worker probe", method, got)
		}
	}
}
