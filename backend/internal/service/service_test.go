package service

import (
	"context"
	"encoding/json"
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
	// blank and errs simulate a partial error: the CLI answers with a full
	// window but an empty section and the reason in Errors.
	blank bool
	errs  map[string]string
}

func (p *countingProvider) Name() string { return p.name }

func (p *countingProvider) Snapshot(_ context.Context, q provider.Query) (*domain.Snapshot, error) {
	p.calls.Add(1)
	time.Sleep(p.delay)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	d := q.From.Format("2006-01-02")
	s := (&domain.Snapshot{
		Meta: domain.Meta{Provider: p.name, From: d, To: q.To.Format("2006-01-02")},
		Lessons: []domain.Lesson{{ID: "1", Date: d, Start: "08:00",
			Subjects: []domain.Element{{Short: "MATH"}}, Rooms: []domain.Element{{Short: p.room}}}},
	}).Ensure()
	if p.blank {
		s.Lessons = []domain.Lesson{}
	}
	if p.errs != nil {
		s.Errors = p.errs
	}
	return s, nil
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

func TestPartialErrorDoesNotReportChanges(t *testing.T) {
	p := &countingProvider{name: "sample", room: "R1"}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	if _, diff, err := svc.Refresh(context.Background(), true); err != nil || len(diff) != 0 {
		t.Fatalf("first refresh: diff=%v err=%v", diff, err)
	}

	// the timetable section fails: empty lessons, reason in Errors
	p.mu.Lock()
	p.blank = true
	p.errs = map[string]string{"timetable": "503 Service Unavailable"}
	p.mu.Unlock()

	snap, diff, err := svc.Refresh(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) != 0 {
		t.Errorf("partial error reported changes: %+v", diff)
	}
	if len(snap.Lessons) != 1 {
		t.Errorf("the last good lessons were dropped: %+v", snap.Lessons)
	}

	// when the CLI recovers, the carried-over lessons are not "added" again
	p.mu.Lock()
	p.blank = false
	p.errs = nil
	p.mu.Unlock()
	if _, diff, err := svc.Refresh(context.Background(), true); err != nil || len(diff) != 0 {
		t.Errorf("recovered refresh: diff=%v err=%v", diff, err)
	}
}

// A partial error on the first refresh has no previous data to carry over, so
// the recovery would report every lesson as added. The diff now skips a section
// that failed on either side.
func TestFirstRefreshWithPartialErrorDoesNotReportChanges(t *testing.T) {
	p := &countingProvider{name: "sample", room: "R1", blank: true,
		errs: map[string]string{"timetable": "503 Service Unavailable"}}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	if _, diff, err := svc.Refresh(context.Background(), true); err != nil || len(diff) != 0 {
		t.Fatalf("first refresh: diff=%v err=%v", diff, err)
	}

	p.mu.Lock()
	p.blank = false
	p.errs = nil
	p.mu.Unlock()
	if _, diff, err := svc.Refresh(context.Background(), true); err != nil || len(diff) != 0 {
		t.Errorf("recovery reported changes: %v err=%v", diff, err)
	}
}

// A reader serialising the cached snapshot while a refresh carries over a
// partial error must not see the write. `go test -race` reports the old version
// of carryOver here.
func TestRefreshDoesNotMutateTheCachedSnapshot(t *testing.T) {
	p := &countingProvider{name: "sample", room: "R1"}
	svc, _ := setup(t, map[string]*countingProvider{"sample": p})
	if _, _, err := svc.Refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			snap, err := svc.Overview(context.Background(), false)
			if err != nil || snap == nil {
				continue
			}
			if _, err := json.Marshal(snap); err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
		}
	}()

	p.mu.Lock()
	p.blank = true
	p.errs = map[string]string{"timetable": "503 Service Unavailable"}
	p.mu.Unlock()
	for i := 0; i < 5; i++ {
		if _, _, err := svc.Refresh(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wg.Wait()
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
