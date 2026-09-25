package pg_test

import (
	"context"
	"testing"
	"time"

	"tide/internal/store/pg"
	"tide/internal/testdb"
)

func sessionFor(t *testing.T, s *pg.Store, id string, ttl time.Duration) int64 {
	t.Helper()
	uid, err := s.Accounts.CreateLocalUser(context.Background(), "alice-"+id, "Alice", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.CreateSession(context.Background(), id, uid, "10.0.0.1", "test", ttl); err != nil {
		t.Fatal(err)
	}
	return uid
}

func expiry(t *testing.T, s *pg.Store, userID int64, id string) time.Time {
	t.Helper()
	list, err := s.Accounts.ListSessions(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == id {
			return x.ExpiresAt
		}
	}
	t.Fatalf("session %s is gone", id)
	return time.Time{}
}

// Being signed out in the middle of working is the thing this fixes: using
// the session has to push its expiry out.
func TestUsingASessionRenewsIt(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	const id = "sess-renew"
	// Two minutes left of a one-hour window: inside the last quarter, so due.
	uid := sessionFor(t, s, id, 2*time.Minute)
	before := expiry(t, s, uid, id)

	if err := s.Accounts.TouchSession(ctx, id, time.Hour, 12*time.Hour); err != nil {
		t.Fatal(err)
	}
	after := expiry(t, s, uid, id)
	if !after.After(before) {
		t.Fatalf("expiry did not move: %s → %s", before, after)
	}
	if got := time.Until(after); got < 50*time.Minute {
		t.Fatalf("renewed to only %s away, want about an hour", got.Round(time.Minute))
	}
}

// Several pages poll on a timer, so "in use" does not need a person: without
// a ceiling a tab left open would keep a session alive for ever.
func TestRenewalStopsAtTheMaximum(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	const id = "sess-capped"
	uid := sessionFor(t, s, id, 2*time.Minute)

	// A cap of five minutes from sign-in, which was a moment ago.
	if err := s.Accounts.TouchSession(ctx, id, time.Hour, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := time.Until(expiry(t, s, uid, id)); got > 6*time.Minute {
		t.Fatalf("renewed %s past the cap of five minutes", got.Round(time.Second))
	}

	// And once the cap is reached, further use does not move it at all.
	first := expiry(t, s, uid, id)
	if err := s.Accounts.TouchSession(ctx, id, time.Hour, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if second := expiry(t, s, uid, id); !second.Equal(first) {
		t.Fatalf("a capped session still moved: %s → %s", first, second)
	}
}

// An expired session is over. Renewing one would turn signing out by waiting
// into something that never happens.
func TestAnExpiredSessionIsNotRevived(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	const id = "sess-dead"
	uid := sessionFor(t, s, id, -time.Minute)

	if err := s.Accounts.TouchSession(ctx, id, time.Hour, 12*time.Hour); err != nil {
		t.Fatal(err)
	}
	if list, err := s.Accounts.ListSessions(ctx, uid); err != nil {
		t.Fatal(err)
	} else if len(list) != 0 {
		t.Fatalf("an expired session came back: %+v", list)
	}
	if u, err := s.Accounts.SessionUser(ctx, id); err != nil || u != nil {
		t.Fatalf("expired session still resolves to a user: %v %v", u, err)
	}
}
