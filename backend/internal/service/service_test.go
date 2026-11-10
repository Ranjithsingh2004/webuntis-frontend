package service

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
	"github.com/seesee010/webuntis-frontend/backend/internal/events"
	"github.com/seesee010/webuntis-frontend/backend/internal/provider"
	"github.com/seesee010/webuntis-frontend/backend/internal/settings"
)

// countingProvider returns a snapshot whose lessons can be changed
// between calls and counts how often it is asked.
type countingProvider struct {
	name  string
	calls atomic.Int32
	delay time.Duration
	mu    sync.Mutex
	room  string
	err   error
}

func (p *countingProvider) Name() string { return p.name }

func (p *countingProvider) Snapshot(ctx context.Context, q provider.Query) (*domain.Snapshot, error) {
	p.calls.Add(1)
	time.Sleep(p.delay)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	d := q.From.Format("2006-01-02")
	return (&domain.Snapshot{
		Meta: domain.Meta{Provider: p.name, From: d, To: q.To.Format("2006-01-02")},
		Lessons: []domain.Lesson{{ID: "1", Date: d, Start: "08:00",
			Subjects: []domain.Element{{Short: "MATH"}}, Rooms: []domain.Element{{Short: p.room}}}},
	}).Ensure(), nil
}

func (p *countingProvider) Who(context.Context, string) ([]domain.Lookup, error) { return nil, nil }

func setup(t *testing.T, providers map[string]*countingProvider) (*Service, *settings.Store) {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := func(b settings.Backend) provider.Provider { return providers[b.Provider] }
	return New(store, f, events.NewBus(), slog.New(slog.NewTextHandler(io.Discard, nil))), store
}

func TestSnapshotIsCachedAndCoalesced(t *testing.T) {
	p := &countingProvider{name: "sample", delay: 20 * time.Millisecond, room: "R1"}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	q := svc.OverviewQuery(false)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = svc.Snapshot(context.Background(), q) }()
	}
	wg.Wait()
	_, _ = svc.Snapshot(context.Background(), q)
	if n := p.calls.Load(); n != 1 {
		t.Errorf("provider called %d times, want 1", n)
	}
	q.Fresh = true
	_, _ = svc.Snapshot(context.Background(), q)
	if n := p.calls.Load(); n != 2 {
		t.Errorf("fresh query must bypass the cache (calls = %d)", n)
	}
}

func TestCancelledCallerDoesNotFailJoinedRequests(t *testing.T) {
	p := &countingProvider{name: "sample", delay: 50 * time.Millisecond}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	q := svc.OverviewQuery(false)

	ctx1, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = svc.Snapshot(ctx1, q) }()
	// let the first caller start the shared call, then abandon it
	time.Sleep(10 * time.Millisecond)
	cancel()

	snap, err := svc.Snapshot(context.Background(), q)
	<-done
	if err != nil {
		t.Fatalf("joined caller failed: %v", err)
	}
	if snap == nil || len(snap.Lessons) != 1 {
		t.Errorf("snapshot = %+v", snap)
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("provider called %d times, want 1", n)
	}
}

func TestRefreshDetectsChanges(t *testing.T) {
	p := &countingProvider{name: "sample", room: "R1"}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	ch, cancel := svc.Bus().Subscribe(10)
	defer cancel()

	if _, diff, err := svc.Refresh(context.Background(), true); err != nil || len(diff) != 0 {
		t.Fatalf("first refresh: diff=%v err=%v", diff, err)
	}
	p.mu.Lock()
	p.room = "R2"
	p.mu.Unlock()
	_, diff, err := svc.Refresh(context.Background(), true)
	if err != nil || len(diff) != 1 || diff[0].Kind != "room-change" {
		t.Fatalf("second refresh: diff=%v err=%v", diff, err)
	}
	var types []string
	for len(ch) > 0 {
		types = append(types, (<-ch).Type)
	}
	want := []string{"snapshot", "snapshot", "changes"}
	if len(types) != 3 || types[2] != want[2] {
		t.Errorf("events = %v, want %v", types, want)
	}
}

func TestRefreshErrorIsReported(t *testing.T) {
	p := &countingProvider{name: "sample", err: provider.Errorf(provider.KindLogin, "bad password")}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	if _, _, err := svc.Refresh(context.Background(), true); err == nil {
		t.Fatal("expected an error")
	}
	st := svc.Status()
	if st.LastError == nil || st.LastError.Kind != provider.KindLogin {
		t.Errorf("status = %+v", st)
	}
}

func TestSwitchingProviderClearsCache(t *testing.T) {
	sample := &countingProvider{name: "sample", room: "R1"}
	cli := &countingProvider{name: "cli", room: "R9"}
	svc, store := setup(t, map[string]*countingProvider{"sample": sample, "cli": cli})
	ch, cancel := svc.Bus().Subscribe(10)
	defer cancel()

	_, _ = svc.Overview(context.Background(), false)
	if err := store.Patch(settings.Document{"backend": map[string]any{"provider": "cli"}}); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.Overview(context.Background(), false)
	if err != nil || snap.Meta.Provider != "cli" {
		t.Fatalf("snapshot from %v, err %v", snap.Meta.Provider, err)
	}
	if svc.Status().Provider != "cli" {
		t.Errorf("status provider = %s", svc.Status().Provider)
	}
	seenProvider := false
	for len(ch) > 0 {
		if (<-ch).Type == events.TypeProvider {
			seenProvider = true
		}
	}
	if !seenProvider {
		t.Error("no provider event")
	}
}
