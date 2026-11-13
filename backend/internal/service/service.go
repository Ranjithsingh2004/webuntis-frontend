// Package service is the backend's application layer. It owns the active
// provider, caches snapshots per date window, refreshes the overview in
// the background and turns consecutive overviews into change events.
package service

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/seesee010/webuntis-frontend/backend/internal/changes"
	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
	"github.com/seesee010/webuntis-frontend/backend/internal/events"
	"github.com/seesee010/webuntis-frontend/backend/internal/provider"
	"github.com/seesee010/webuntis-frontend/backend/internal/settings"
)

// Factory builds the provider for the given backend settings.
type Factory func(settings.Backend) provider.Provider

// Status describes the state of the service for the frontend.
type Status struct {
	Provider    string           `json:"provider"`
	LastRefresh *time.Time       `json:"lastRefresh"`
	NextRefresh *time.Time       `json:"nextRefresh"`
	LastError   *ErrorInfo       `json:"lastError"`
	Settings    settings.Backend `json:"backend"`
}

// ErrorInfo is a provider error in a JSON friendly form.
type ErrorInfo struct {
	Kind    provider.Kind `json:"kind"`
	Message string        `json:"message"`
	At      time.Time     `json:"at"`
}

// NewErrorInfo classifies err.
func NewErrorInfo(err error) *ErrorInfo {
	return &ErrorInfo{Kind: provider.KindOf(err), Message: err.Error(), At: time.Now()}
}

type cacheEntry struct {
	snap *domain.Snapshot
	at   time.Time
}

type call struct {
	done chan struct{}
	snap *domain.Snapshot
	err  error
}

// Service coordinates providers, caching and refreshes.
type Service struct {
	store   *settings.Store
	factory Factory
	bus     *events.Bus
	log     *slog.Logger
	now     func() time.Time

	mu          sync.Mutex
	cfg         settings.Backend
	prov        provider.Provider
	gen         int // bumped whenever the provider changes; stale results are dropped
	cache       map[string]cacheEntry
	inflight    map[string]*call
	overview    *domain.Snapshot
	lastRefresh time.Time
	nextRefresh time.Time
	lastErr     *ErrorInfo
	kick        chan struct{}
}

// New creates the service and subscribes it to settings changes.
func New(store *settings.Store, factory Factory, bus *events.Bus, log *slog.Logger) *Service {
	s := &Service{
		store: store, factory: factory, bus: bus, log: log, now: time.Now,
		cache: map[string]cacheEntry{}, inflight: map[string]*call{},
		kick: make(chan struct{}, 1),
	}
	s.cfg = store.Backend()
	s.prov = factory(s.cfg)
	store.Subscribe(s.onSettings)
	return s
}

// Bus returns the event bus.
func (s *Service) Bus() *events.Bus { return s.bus }

func (s *Service) onSettings(doc settings.Document) {
	cfg := settings.BackendFrom(doc)
	s.bus.Publish(events.TypeSettings, nil)

	s.mu.Lock()
	if reflect.DeepEqual(cfg, s.cfg) {
		s.mu.Unlock()
		return
	}
	providerChanged := !reflect.DeepEqual(cfg.CLI, s.cfg.CLI) || cfg.Provider != s.cfg.Provider ||
		cfg.Sample != s.cfg.Sample
	s.cfg = cfg
	if providerChanged {
		s.prov = s.factory(cfg)
		s.gen++
		s.cache = map[string]cacheEntry{}
		s.overview = nil
		s.lastErr = nil
	}
	name := s.prov.Name()
	s.mu.Unlock()

	if providerChanged {
		s.log.Info("provider switched", "provider", name)
		s.bus.Publish(events.TypeProvider, map[string]string{"provider": name})
	}
	s.Kick()
}

// Kick asks the background loop to refresh now.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Status returns the current state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Provider: s.prov.Name(), LastError: s.lastErr, Settings: s.cfg}
	if !s.lastRefresh.IsZero() {
		t := s.lastRefresh
		st.LastRefresh = &t
	}
	if !s.nextRefresh.IsZero() {
		t := s.nextRefresh
		st.NextRefresh = &t
	}
	return st
}

// ttl is how long a cached window stays valid without a refresh.
func (s *Service) ttl() time.Duration {
	if s.cfg.RefreshMinutes <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(s.cfg.RefreshMinutes) * time.Minute
}

// Snapshot returns the data for a window, from the cache when possible.
// Concurrent requests for the same window share one provider call.
func (s *Service) Snapshot(ctx context.Context, q provider.Query) (*domain.Snapshot, error) {
	key := q.From.Format("2006-01-02") + ".." + q.To.Format("2006-01-02")

	s.mu.Lock()
	if e, ok := s.cache[key]; ok && !q.Fresh && s.now().Sub(e.at) < s.ttl() {
		s.mu.Unlock()
		return e.snap, nil
	}
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.snap, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	prov, gen := s.prov, s.gen
	s.mu.Unlock()

	c.snap, c.err = prov.Snapshot(ctx, q)

	s.mu.Lock()
	delete(s.inflight, key)
	if c.err == nil && gen == s.gen {
		s.cache[key] = cacheEntry{snap: c.snap, at: s.now()}
	}
	s.mu.Unlock()
	close(c.done)
	return c.snap, c.err
}

// OverviewQuery is the window used for the dashboard and list views.
func (s *Service) OverviewQuery(fresh bool) provider.Query {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	today := day(s.now())
	return provider.Query{
		From:  today.AddDate(0, 0, -cfg.DaysBack),
		To:    today.AddDate(0, 0, cfg.DaysAhead),
		Fresh: fresh,
	}
}

// Overview returns the overview snapshot.
func (s *Service) Overview(ctx context.Context, fresh bool) (*domain.Snapshot, error) {
	return s.Snapshot(ctx, s.OverviewQuery(fresh))
}

// Refresh refetches the overview, diffs it against the previous one and
// publishes the result. It is what the background loop and the
// "refresh" button run.
func (s *Service) Refresh(ctx context.Context, fresh bool) (*domain.Snapshot, []changes.Change, error) {
	s.mu.Lock()
	gen := s.gen
	s.mu.Unlock()

	snap, err := s.Overview(ctx, fresh)

	s.mu.Lock()
	if gen != s.gen { // provider switched meanwhile; that switch triggers its own refresh
		s.mu.Unlock()
		return snap, nil, err
	}
	if err != nil {
		s.lastErr = NewErrorInfo(err)
		info := s.lastErr
		s.mu.Unlock()
		s.log.Warn("refresh failed", "err", err)
		s.bus.Publish(events.TypeError, info)
		return nil, nil, err
	}
	// A partial error arrives with a full window and an empty section, so the
	// previous data is carried over before diffing. carryOver returns a new
	// snapshot rather than editing this one: snap is already in the cache and
	// other callers hold it.
	fetched := snap
	snap = carryOver(s.overview, snap)
	var diff []changes.Change
	if s.cfg.DetectChanges && s.overview != nil && s.overview != snap {
		diff = withoutFailedSections(s.overview, snap, changes.Diff(s.overview, snap))
	}
	s.overview = snap
	s.replaceCached(fetched, snap)
	s.lastErr = nil
	s.lastRefresh = s.now()
	s.mu.Unlock()

	s.bus.Publish(events.TypeSnapshot, map[string]any{"from": snap.Meta.From, "to": snap.Meta.To})
	if len(diff) > 0 {
		s.bus.Publish(events.TypeChanges, diff)
	}
	return snap, diff, nil
}

// carryOver copies the sections the CLI could not fetch from the previous
// snapshot. A partial error (timetable.error, exams.error, …) still returns a
// full Meta window with an empty section, so diffing it would report every
// lesson as dropped and every exam as removed, and the next successful refresh
// would report them all as added again. Keeping the last good data also stops
// the views from emptying on a short hiccup.
func carryOver(prev, cur *domain.Snapshot) *domain.Snapshot {
	if prev == nil || cur == nil || len(cur.Errors) == 0 {
		return cur
	}
	// Work on a copy. cur is already in the cache and handed to other callers,
	// so writing into it raced every reader that serialised it: a reader that
	// held the cached snapshot and called json.Marshal while Refresh edited the
	// sections was reported by `go test -race`.
	merged := *cur
	if _, failed := cur.Errors["timetable"]; failed {
		merged.TimeGrid = prev.TimeGrid
		merged.Lessons = prev.Lessons
	}
	if _, failed := cur.Errors["exams"]; failed {
		merged.Exams = prev.Exams
	}
	if _, failed := cur.Errors["homework"]; failed {
		merged.Homework = prev.Homework
	}
	if _, failed := cur.Errors["absences"]; failed {
		merged.Absences = prev.Absences
	}
	if _, failed := cur.Errors["messages"]; failed {
		merged.Messages = prev.Messages
	}
	if _, failed := cur.Errors["holidays"]; failed {
		merged.Holidays = prev.Holidays
	}
	return &merged
}

// replaceCached makes next the cached answer wherever old was cached, so a
// caller that asks again gets the snapshot this refresh settled on. The caller
// holds s.mu.
func (s *Service) replaceCached(old, next *domain.Snapshot) {
	if old == next {
		return
	}
	for key, entry := range s.cache {
		if entry.snap == old {
			entry.snap = next
			s.cache[key] = entry
		}
	}
}

// sectionKinds lists the change kinds each CLI section produces.
var sectionKinds = map[string][]string{
	"timetable": {changes.Cancelled, changes.Substitution, changes.RoomChange, changes.Removed, changes.LessonAdded, changes.LessonDropped},
	"exams":     {changes.ExamAdded, changes.ExamRemoved},
	"homework":  {changes.HomeworkAdded},
	"messages":  {changes.MessageNew},
}

// withoutFailedSections drops the changes belonging to a section the CLI could
// not fetch on either side. carryOver covers a section that fails after a good
// refresh, but the first refresh after start has nothing to carry over, so its
// recovery would report every lesson as added.
func withoutFailedSections(prev, cur *domain.Snapshot, diff []changes.Change) []changes.Change {
	failed := map[string]bool{}
	for _, snap := range []*domain.Snapshot{prev, cur} {
		if snap == nil {
			continue
		}
		for section := range snap.Errors {
			for _, kind := range sectionKinds[section] {
				failed[kind] = true
			}
		}
	}
	if len(failed) == 0 {
		return diff
	}
	kept := make([]changes.Change, 0, len(diff))
	for _, c := range diff {
		if !failed[c.Kind] {
			kept = append(kept, c)
		}
	}
	return kept
}

// Run refreshes in the background until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		_, _, _ = s.Refresh(ctx, false)

		s.mu.Lock()
		interval := time.Duration(s.cfg.RefreshMinutes) * time.Minute
		var timer <-chan time.Time
		if interval > 0 {
			s.nextRefresh = s.now().Add(interval)
			timer = time.After(interval)
		} else {
			s.nextRefresh = time.Time{}
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-timer:
		case <-s.kick:
		}
	}
}

// Who looks up a code with the active provider.
func (s *Service) Who(ctx context.Context, code string) ([]domain.Lookup, error) {
	s.mu.Lock()
	prov := s.prov
	s.mu.Unlock()
	return prov.Who(ctx, code)
}

// Provider returns the active provider.
func (s *Service) Provider() provider.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prov
}

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
