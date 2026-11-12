package cmdparse

import (
	"sync"
	"testing"
)

// Parsing is called from concurrent requests; it must not share mutable state (run with -race).
func TestParseIsSafeConcurrently(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			for j := 0; j < 50; j++ {
				Parse("echo hello && ls -la /tmp | wc -l")
			}
		})
	}
	wg.Wait()
}
