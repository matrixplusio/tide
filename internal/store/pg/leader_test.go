package pg_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tide/internal/store"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

const probeLock = 7419099

// appURL is the same connection string testdb used, for a second pool.
func appURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TIDE_TEST_APP_URL")
	if u == "" {
		t.Skip("needs TIDE_TEST_APP_URL")
	}
	return u
}

// Two replicas, one loop: exactly one of them may be doing the work at any
// moment. Without this the executor would create a second Kargo promotion
// for an item another replica is already promoting.
func TestOnlyOneReplicaLeads(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var inWork, maxConcurrent int32
	var runs [2]atomic.Int32
	work := func(i int) func(context.Context) {
		return func(context.Context) {
			n := atomic.AddInt32(&inWork, 1)
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if n <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, n) {
					break
				}
			}
			runs[i].Add(1)
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inWork, -1)
		}
	}

	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pg.Lead(ctx, s.DB(), probeLock, 10*time.Millisecond, "probe", work(i))
		}()
	}
	time.Sleep(400 * time.Millisecond)
	cancel()
	wg.Wait()

	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("%d replicas were working at once, want 1", got)
	}
	a, b := runs[0].Load(), runs[1].Load()
	if a+b == 0 {
		t.Fatal("neither replica did any work")
	}
	if a > 0 && b > 0 {
		t.Fatalf("both replicas did work (%d and %d); leadership was not exclusive", a, b)
	}
}

// Leadership must move when the leader goes away, or a replica dying would
// stop releases from ever being advanced.
func TestLeadershipMovesWhenTheLeaderStops(t *testing.T) {
	s, _ := testdb.Setup(t)
	// A separate pool so cancelling the first leader really closes its
	// connection, the way a pod being deleted would.
	other, err := store.Open(context.Background(), appURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := other.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	firstCtx, stopFirst := context.WithCancel(context.Background())
	secondCtx, stopSecond := context.WithCancel(context.Background())
	defer stopSecond()

	var first, second atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pg.Lead(firstCtx, s.DB(), probeLock, 10*time.Millisecond, "first", func(context.Context) { first.Add(1) })
	}()
	// Let the first one take the lock before the second starts asking.
	time.Sleep(150 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		pg.Lead(secondCtx, other, probeLock, 10*time.Millisecond, "second", func(context.Context) { second.Add(1) })
	}()
	time.Sleep(150 * time.Millisecond)

	if first.Load() == 0 {
		t.Fatal("the first replica never led")
	}
	if n := second.Load(); n != 0 {
		t.Fatalf("the second replica worked %d times while the first held the lock", n)
	}

	stopFirst()
	time.Sleep(300 * time.Millisecond)
	if second.Load() == 0 {
		t.Fatal("the second replica never took over after the first stopped")
	}
	stopSecond()
	wg.Wait()
}

// The two loops must not contend: the executor and the CI worker may lead on
// different replicas.
func TestTheTwoLoopsUseDifferentLocks(t *testing.T) {
	if pg.LockExecutor == pg.LockCIIntake {
		t.Fatalf("both loops use lock %d, so only one of them can ever run", pg.LockExecutor)
	}
}
