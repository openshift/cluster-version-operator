package internal

import (
	"sync"
	"testing"
)

// TestAddSchemesConcurrent verifies that AddSchemes can be called concurrently
// without causing race conditions or panics. This is a regression test for
// "fatal error: concurrent map writes" when integration tests run in parallel.
func TestAddSchemesConcurrent(t *testing.T) {
	const numGoroutines = 10

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	// Call AddSchemes concurrently from multiple goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := AddSchemes(); err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check if any goroutine encountered an error
	for err := range errChan {
		t.Errorf("AddSchemes failed: %v", err)
	}
}
