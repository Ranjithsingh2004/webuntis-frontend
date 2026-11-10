package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestMissingFileStartsEmpty(t *testing.T) {
	s, path := open(t)
	if len(s.Get()) != 0 {
		t.Errorf("doc = %v", s.Get())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Open must not create the file")
	}
}

func TestPatchMergesAndPersists(t *testing.T) {
	s, path := open(t)
	must(t, s.Replace(Document{"ui": map[string]any{"theme": "dark", "density": "cozy"}, "keep": 1.0}))
	must(t, s.Patch(Document{"ui": map[string]any{"theme": "light", "density": nil}, "new": []any{"a"}}))

	want := Document{"ui": map[string]any{"theme": "light"}, "keep": 1.0, "new": []any{"a"}, "$version": 1.0}
	if got := s.Get(); !reflect.DeepEqual(got, want) {
		t.Errorf("doc = %v, want %v", got, want)
	}

	again, err := Open(path)
	must(t, err)
	if !reflect.DeepEqual(again.Get(), want) {
		t.Errorf("reloaded = %v", again.Get())
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", dir.Mode().Perm())
	}
}

func TestSubscribersSeeWritesInOrder(t *testing.T) {
	s, _ := open(t)

	var mu sync.Mutex
	var seen []string
	release := make(chan struct{})
	entered := make(chan struct{})
	// a plain flag, not sync.Once: Do holds its own lock, which would make the
	// second notification wait here as well and hide the ordering bug
	var flagMu sync.Mutex
	first := true
	s.Subscribe(func(doc Document) {
		flagMu.Lock()
		isFirst := first
		first = false
		flagMu.Unlock()
		if isFirst {
			close(entered)
			<-release
		}
		mu.Lock()
		seen = append(seen, doc["mark"].(string))
		mu.Unlock()
	})

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); errs <- s.Patch(Document{"mark": "A"}) }()
	<-entered // A is being delivered and is held here
	wg.Add(1)
	go func() { defer wg.Done(); errs <- s.Patch(Document{"mark": "B"}) }()
	// let B finish its write and reach the notification
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[len(seen)-1] != "B" {
		t.Errorf("subscribers saw %v, the newest write must be delivered last", seen)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	s, _ := open(t)
	must(t, s.Replace(Document{"ui": map[string]any{"theme": "dark"}}))
	doc := s.Get()
	doc["ui"].(map[string]any)["theme"] = "changed"
	if s.Get()["ui"].(map[string]any)["theme"] != "dark" {
		t.Error("Get leaked internal state")
	}
}

func TestResetAndSubscribe(t *testing.T) {
	s, _ := open(t)
	var seen []Document
	s.Subscribe(func(d Document) { seen = append(seen, d) })
	must(t, s.Patch(Document{"a": 1.0}))
	must(t, s.Reset())
	if len(seen) != 2 || seen[0]["a"] != 1.0 || len(seen[1]) != 1 {
		t.Errorf("notifications = %v", seen)
	}
}

func TestInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	must(t, os.WriteFile(path, []byte("{nope"), 0o600))
	if _, err := Open(path); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestBackendDefaultsAndOverrides(t *testing.T) {
	if got := BackendFrom(Document{}); !reflect.DeepEqual(got, DefaultBackend()) {
		t.Errorf("defaults = %+v", got)
	}
	var doc Document
	must(t, json.Unmarshal([]byte(`{"backend": {"provider": "cli", "refreshMinutes": 3,
		"cli": {"binary": "/opt/untis", "extraArgs": ["--long"]}}}`), &doc))
	b := BackendFrom(doc)
	if b.Provider != "cli" || b.RefreshMinutes != 3 || b.CLI.Binary != "/opt/untis" ||
		b.CLI.Transport != "auto" || b.CLI.TimeoutSec != 120 || b.DaysAhead != 60 {
		t.Errorf("backend = %+v", b)
	}
	if BackendFrom(Document{"backend": map[string]any{"provider": "evil"}}).Provider != "sample" {
		t.Error("unknown provider must fall back to sample")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
