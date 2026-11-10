package cli

import (
	"context"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seesee010/webuntis-frontend/backend/internal/provider"
)

// fakeRunner records calls and answers with a canned result.
type fakeRunner struct {
	res   Result
	err   error
	name  string
	calls [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string) (Result, error) {
	f.name = name
	f.calls = append(f.calls, args)
	return f.res, f.err
}

var window = provider.Query{
	From: time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local),
	To:   time.Date(2026, 10, 9, 0, 0, 0, 0, time.Local),
}

func TestSnapshotArgs(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		q    provider.Query
		want string
	}{
		{"plain", Config{}, window, "--json - --from 2026-10-05 --to 2026-10-09"},
		{"all options", Config{ConfigPath: "c.json", EnvPath: ".env", Transport: "http", MaxAge: "10m", ExtraArgs: []string{"--long"}},
			window, "--config c.json --env .env --transport http --json - --from 2026-10-05 --to 2026-10-09 --max-age 10m --long"},
		{"fresh skips max-age", Config{MaxAge: "10m"}, provider.Query{From: window.From, To: window.To, Fresh: true},
			"--json - --from 2026-10-05 --to 2026-10-09"},
		{"offline wins", Config{MaxAge: "10m", Offline: true}, window,
			"--json - --from 2026-10-05 --to 2026-10-09 --offline"},
	}
	for _, c := range cases {
		got := strings.Join(New(c.cfg, &fakeRunner{}).SnapshotArgs(c.q), " ")
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestSnapshotParsesOutput(t *testing.T) {
	data, err := os.ReadFile("../../untisjson/testdata/rest_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{res: Result{Stdout: data}}
	s, err := New(Config{Binary: "/opt/untis"}, r).Snapshot(context.Background(), window)
	if err != nil {
		t.Fatal(err)
	}
	if r.name != "/opt/untis" {
		t.Errorf("binary = %q", r.name)
	}
	if s.Meta.Provider != "cli" || len(s.Lessons) != 4 {
		t.Errorf("snapshot = %+v", s.Meta)
	}
}

func TestExitCodes(t *testing.T) {
	cases := map[int]provider.Kind{
		2: provider.KindConfig, 3: provider.KindLogin, 4: provider.KindUnreachable,
		5: provider.KindNoSchool, 6: provider.KindNotFound, 130: provider.KindCancelled, 1: provider.KindInternal,
	}
	for code, want := range cases {
		r := &fakeRunner{res: Result{ExitCode: code, Stderr: []byte("some log\nuntis: login failed: bad password\n")}}
		_, err := New(Config{}, r).Snapshot(context.Background(), window)
		if provider.KindOf(err) != want {
			t.Errorf("exit %d: kind = %v, want %v", code, provider.KindOf(err), want)
		}
		if err.Error() != "login failed: bad password" {
			t.Errorf("exit %d: message = %q", code, err.Error())
		}
	}
}

func TestMissingBinaryIsAConfigError(t *testing.T) {
	r := &fakeRunner{err: &fs.PathError{Op: "fork/exec", Path: "/nope", Err: fs.ErrNotExist}}
	_, err := New(Config{Binary: "/nope"}, r).Snapshot(context.Background(), window)
	if provider.KindOf(err) != provider.KindConfig || !strings.Contains(err.Error(), "pipx") {
		t.Errorf("err = %v (%v)", err, provider.KindOf(err))
	}
}

func TestBadJSON(t *testing.T) {
	r := &fakeRunner{res: Result{Stdout: []byte("not json")}}
	_, err := New(Config{}, r).Snapshot(context.Background(), window)
	if provider.KindOf(err) != provider.KindInternal {
		t.Errorf("err = %v", err)
	}
}

func TestWho(t *testing.T) {
	r := &fakeRunner{res: Result{Stdout: []byte("TCH1   teacher   Sample Teacher\nR101   room      Classroom, building T\nnoise\n")}}
	cfg := Config{Offline: true, ExtraArgs: []string{"--profile", "school2"}}
	got, err := New(cfg, r).Who(context.Background(), "TCH1")
	if err != nil {
		t.Fatal(err)
	}
	// the extra arguments belong to every data call, the code lookup included
	if want := []string{"--who", "TCH1", "--offline", "--profile", "school2"}; !reflect.DeepEqual(r.calls[0], want) {
		t.Errorf("args = %v, want %v", r.calls[0], want)
	}
	if len(got) != 2 || got[0].Kind != "teacher" || got[1].Name != "Classroom, building T" {
		t.Errorf("lookups = %+v", got)
	}
}

func TestExecRunnerExitCode(t *testing.T) {
	res, err := ExecRunner{}.Run(context.Background(), "sh", []string{"-c", "echo out; echo err >&2; exit 3"})
	if err != nil {
		t.Skip("no sh available:", err)
	}
	if res.ExitCode != 3 || strings.TrimSpace(string(res.Stdout)) != "out" || strings.TrimSpace(string(res.Stderr)) != "err" {
		t.Errorf("res = %+v", res)
	}
}
