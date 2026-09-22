package setup_test

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"tide/internal/access"
	"tide/internal/auth"
	"tide/internal/crypto"
	"tide/internal/settings"
	"tide/internal/setup"
	"tide/internal/testdb"
)

// Replicas starting together must each announce the token that actually
// works, not the one they happened to generate. They all find no setup row,
// they all generate a candidate, and only one insert can win: a replica that
// announces its own candidate sends whoever reads the log to try a value that
// will never be accepted. The log is the entire interface here — there is
// nowhere else to read the token from — so announcing a dead one is the same
// as announcing nothing.
//
// The assertion is deliberately not "everyone printed the same string": with
// replicas released at slightly different moments, the first one usually
// inserts before the others have looked, so they never reach the generating
// branch and a broken version agrees with itself. What has to hold is that
// every announced token is the stored one.
func TestConcurrentInitAnnouncesTheTokenThatWorks(t *testing.T) {
	store, _ := testdb.Setup(t)
	box, err := crypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	newService := func() *setup.Service {
		// Each replica is its own process with its own settings cache; the
		// database underneath is the one thing they share.
		set := &settings.Store{PG: store, Box: box}
		a := &auth.Service{PG: store, Settings: set, Box: box, Access: &access.Service{PG: store, Settings: set}}
		return &setup.Service{PG: store, Settings: set, Auth: a}
	}

	// The announcement goes through the global logger, which is where an
	// operator reads it; observing that is observing the real interface.
	core, logs := observer.New(zap.WarnLevel)
	defer zap.ReplaceGlobals(zap.New(core))()

	// More replicas than a real deployment, all released at once, so that
	// several of them look before the first insert lands.
	const replicas = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, replicas)
	for i := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := newService()
			<-start
			errs[i] = s.Init(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d failed to start: %v", i, err)
		}
	}

	var announced []string
	for _, e := range logs.All() {
		for _, f := range e.Context {
			if f.Key == "setup_token" {
				announced = append(announced, f.String)
			}
		}
	}
	if len(announced) == 0 {
		t.Fatal("no replica announced a setup token; nobody could finish setup")
	}
	// CheckToken reads what is stored, so this asks the question an operator
	// asks: does the thing in the log actually get me in?
	checker := newService()
	if err := checker.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, tok := range announced {
		if !checker.CheckToken(context.Background(), tok) {
			t.Fatalf("announcement %d of %d is a token that does not work: %q", i+1, len(announced), tok)
		}
	}
}
