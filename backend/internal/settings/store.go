// Package settings persists the GUI settings.
//
// The settings are one JSON document. The frontend owns most of its
// schema (each frontend module declares its own settings), so the store
// treats the document as an opaque tree and only interprets the
// "backend" subtree it needs itself (see Backend).
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Version of the document layout, stored as "$version".
const Version = 1

const versionKey = "$version"

// Document is a settings tree.
type Document = map[string]any

// Store keeps the settings in memory and on disk.
type Store struct {
	path string

	mu   sync.RWMutex
	doc  Document
	subs []func(Document)

	// notifyMu keeps the notifications in write order, see notify.
	notifyMu sync.Mutex
	seq      int
	notified int
}

// DefaultPath is $XDG_CONFIG_HOME/untis-gui/settings.json (or the
// platform equivalent).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "untis-gui", "settings.json"), nil
}

// Open loads the settings at path. A missing file is not an error: the
// store starts empty and creates the file on the first write.
func Open(path string) (*Store, error) {
	s := &Store{path: path, doc: Document{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.doc); err != nil {
		return nil, fmt.Errorf("settings: %s is not valid JSON: %w", path, err)
	}
	if s.doc == nil {
		s.doc = Document{}
	}
	migrate(s.doc)
	return s, nil
}

// Path is where the settings are stored.
func (s *Store) Path() string { return s.path }

// Get returns a copy of the whole document.
func (s *Store) Get() Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.doc)
}

// Replace overwrites the document.
func (s *Store) Replace(doc Document) error {
	if doc == nil {
		doc = Document{}
	}
	return s.update(func(Document) Document { return clone(doc) })
}

// Patch applies a JSON merge patch (RFC 7386): objects are merged
// recursively, null removes a key, everything else replaces.
func (s *Store) Patch(patch Document) error {
	return s.update(func(cur Document) Document { return mergePatch(cur, patch) })
}

// Reset removes all settings, so every default applies again.
func (s *Store) Reset() error {
	return s.update(func(Document) Document { return Document{} })
}

// Subscribe registers fn to be called with a copy of the document after
// every change.
func (s *Store) Subscribe(fn func(Document)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

func (s *Store) update(change func(Document) Document) error {
	s.mu.Lock()
	next := change(clone(s.doc))
	next[versionKey] = float64(Version)
	if err := writeAtomic(s.path, next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.doc = next
	s.seq++
	seq := s.seq
	subs := append([]func(Document){}, s.subs...)
	snapshot := clone(next)
	s.mu.Unlock()

	s.notify(seq, subs, snapshot)
	return nil
}

// notify delivers a write to the subscribers in order. Two writes A and B can
// reach this point in either order once the lock is released, and a subscriber
// that acts on the document (the service rebuilds the provider from it) would
// then keep applying the older one, leaving the backend on stale settings. A
// notification is dropped when a newer document has already been delivered:
// that newer document contains the older write anyway.
func (s *Store) notify(seq int, subs []func(Document), doc Document) {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if seq < s.notified {
		return
	}
	s.notified = seq
	for _, fn := range subs {
		fn(clone(doc))
	}
}

// writeAtomic writes the document to a temporary file next to path and
// renames it into place, so a crash never leaves half a file behind.
// The settings may contain paths to the user's untis config, so the file
// is private (0600 in a 0700 directory).
func writeAtomic(path string, doc Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// migrate upgrades older document layouts in place. There is only one
// version so far; this is where future renames go.
func migrate(doc Document) {
	if _, ok := doc[versionKey]; !ok {
		doc[versionKey] = float64(Version)
	}
}

func mergePatch(target Document, patch Document) Document {
	if target == nil {
		target = Document{}
	}
	for k, v := range patch {
		if v == nil {
			delete(target, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			tm, _ := target[k].(map[string]any)
			target[k] = mergePatch(tm, pm)
			continue
		}
		target[k] = cloneValue(v)
	}
	return target
}

func clone(doc Document) Document {
	out := make(Document, len(doc))
	for k, v := range doc {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return clone(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = cloneValue(x)
		}
		return out
	default:
		return v
	}
}
