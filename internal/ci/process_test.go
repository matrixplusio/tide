package ci

import (
	"context"
	"sync"
	"testing"
)

// Two passes over the same waiting intake each create a release, and only one
// of them can claim the target: the other is left as a draft nobody asked
// for, beside a real release for the same service a second earlier. Eight of
// those accumulated on one installation before anybody noticed.
//
// The pass that finds another already running does nothing at all — it does
// not queue, because whatever it would have found is what the running one is
// already working through.
func TestOnlyOnePassRunsAtATime(t *testing.T) {
	s := &Service{}
	if !s.pass.CompareAndSwap(false, true) {
		t.Fatal("a fresh service must not already be mid-pass")
	}

	// While one is held, every other caller turns back.
	var wg sync.WaitGroup
	got := make(chan bool, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got <- s.pass.CompareAndSwap(false, true)
		}()
	}
	wg.Wait()
	close(got)
	for ok := range got {
		if ok {
			t.Fatal("two passes ran over the same waiting intakes")
		}
	}

	// And the next one after it finishes goes ahead: a pass must not be lost.
	s.pass.Store(false)
	if !s.pass.CompareAndSwap(false, true) {
		t.Error("the guard stayed shut after the pass finished")
	}
}

// Process is what the guard protects; calling it on a service with no
// database must return rather than panic, because the guard runs first.
func TestProcessWithNothingBehindItIsHarmless(t *testing.T) {
	s := &Service{}
	s.pass.Store(true) // pretend a pass is running
	s.Process(context.Background())
	if !s.pass.Load() {
		t.Error("the running pass's guard was cleared by the one that turned back")
	}
}
