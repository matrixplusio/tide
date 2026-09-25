package ci

import (
	"testing"
	"time"
)

// The nudge on arrival is one request, and one request can fail. After that
// the only thing left was Kargo's own interval, and four intakes expired
// with their image sitting in the registry the whole time. The waiting loop
// is already awake and already knows the freight has not arrived, so it asks
// again — spaced out, because ninety requests per intake is not a retry, it
// is a flood.
func TestTheWarehouseIsAskedAgainWhileWaiting(t *testing.T) {
	var at []time.Duration
	for i := 0; i <= int(DefaultWait/DefaultTick); i++ {
		if renudgeAt(i) {
			at = append(at, time.Duration(i)*DefaultTick)
		}
	}
	if len(at) < 3 {
		t.Fatalf("only %d retries in a wait of %s: %v", len(at), DefaultWait, at)
	}
	if first := at[0]; first > 2*time.Minute {
		t.Errorf("the first retry is %s in; a lost notification should be recovered while somebody still cares", first)
	}
	if last := at[len(at)-1]; last >= DefaultWait {
		t.Errorf("the last retry at %s falls outside the wait of %s", last, DefaultWait)
	}
	// Spaced out, not fixed: each gap larger than the one before it.
	for i := 2; i < len(at); i++ {
		if at[i]-at[i-1] <= at[i-1]-at[i-2] {
			t.Errorf("retries are not backing off: %v", at)
			break
		}
	}
	// And far fewer than one per pass.
	if len(at) > 6 {
		t.Errorf("%d retries is too many for one intake: %v", len(at), at)
	}
}

// Every pass would be a request every twenty seconds per waiting intake.
func TestItDoesNotRetryOnEveryPass(t *testing.T) {
	for _, n := range []int{0, 1, 2, 4, 5, 10, 50} {
		if renudgeAt(n) {
			t.Errorf("attempt %d should not trigger a retry", n)
		}
	}
}
