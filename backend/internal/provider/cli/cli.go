// Package cli implements a provider that drives the installed untis CLI
// (https://github.com/seesee010/webuntis-scraper). The GUI never talks to
// WebUntis itself: login, session handling, caching and the API calls
// all stay in the CLI.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
	"github.com/seesee010/webuntis-frontend/backend/internal/provider"
	"github.com/seesee010/webuntis-frontend/backend/internal/untisjson"
)

// Exit codes documented by the untis CLI.
const (
	exitConfig      = 2
	exitLogin       = 3
	exitUnreachable = 4
	exitNoSchool    = 5
	exitNotFound    = 6
	exitInterrupted = 130
)

// Config controls how the CLI is invoked.
type Config struct {
	// Binary is the untis executable; "untis" (looked up in PATH) if empty.
	Binary string
	// ConfigPath and EnvPath are passed as --config / --env when set.
	ConfigPath string
	EnvPath    string
	// Transport is "auto", "http" or "browser"; empty keeps the CLI default.
	Transport string
	// MaxAge lets the CLI answer from its cache, e.g. "10m".
	MaxAge string
	// Offline answers only from the CLI cache and never hits the network.
	Offline bool
	// ExtraArgs are appended verbatim, for options the GUI has no field for.
	ExtraArgs []string
	// Timeout bounds every invocation; 0 means 2 minutes.
	Timeout time.Duration
}

// Provider runs the untis CLI.
type Provider struct {
	cfg Config
	run Runner
	now func() time.Time
}

// New creates a CLI provider. A nil runner uses ExecRunner.
func New(cfg Config, run Runner) *Provider {
	if run == nil {
		run = ExecRunner{Env: []string{"NO_COLOR=1"}}
	}
	if cfg.Binary == "" {
		cfg.Binary = "untis"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	return &Provider{cfg: cfg, run: run, now: time.Now}
}

func (p *Provider) Name() string { return "cli" }

// SnapshotArgs builds the arguments of a snapshot call.
func (p *Provider) SnapshotArgs(q provider.Query) []string {
	args := p.commonArgs()
	args = append(args, "--json", "-",
		"--from", q.From.Format("2006-01-02"),
		"--to", q.To.Format("2006-01-02"))
	if p.cfg.Offline {
		args = append(args, "--offline")
	} else if p.cfg.MaxAge != "" && !q.Fresh {
		args = append(args, "--max-age", p.cfg.MaxAge)
	}
	return append(args, p.cfg.ExtraArgs...)
}

func (p *Provider) commonArgs() []string {
	var args []string
	if p.cfg.ConfigPath != "" {
		args = append(args, "--config", p.cfg.ConfigPath)
	}
	if p.cfg.EnvPath != "" {
		args = append(args, "--env", p.cfg.EnvPath)
	}
	if p.cfg.Transport != "" {
		args = append(args, "--transport", p.cfg.Transport)
	}
	return args
}

// Snapshot runs `untis --json - --from … --to …` and converts the output.
func (p *Provider) Snapshot(ctx context.Context, q provider.Query) (*domain.Snapshot, error) {
	res, err := p.exec(ctx, p.SnapshotArgs(q))
	if err != nil {
		return nil, err
	}
	payload, err := untisjson.Parse(res.Stdout)
	if err != nil {
		return nil, &provider.Error{Kind: provider.KindInternal, Msg: "unexpected output of untis --json", Err: err}
	}
	return payload.ToSnapshot(p.Name(), p.now()), nil
}

// Who runs `untis --who CODE`. The extra arguments are added here as well:
// they carry options the GUI has no field for (a school profile, a proxy, a
// cache directory), and without them the lookup answers for another profile or
// fails although the snapshot call works.
func (p *Provider) Who(ctx context.Context, code string) ([]domain.Lookup, error) {
	args := append(p.commonArgs(), "--who", code)
	if p.cfg.Offline {
		args = append(args, "--offline")
	}
	args = append(args, p.cfg.ExtraArgs...)
	res, err := p.exec(ctx, args)
	if err != nil {
		return nil, err
	}
	return ParseWho(res.Stdout), nil
}

// Version runs `untis --version`, e.g. to show it in the settings.
func (p *Provider) Version(ctx context.Context) (string, error) {
	res, err := p.exec(ctx, []string{"--version"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (p *Provider) exec(ctx context.Context, args []string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	res, err := p.run.Run(ctx, p.cfg.Binary, args)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return res, &provider.Error{
				Kind: provider.KindConfig,
				Msg:  "untis CLI not found (" + p.cfg.Binary + "); install it with: pipx install git+https://github.com/seesee010/webuntis-scraper",
				Err:  err,
			}
		}
		if ctx.Err() != nil {
			return res, &provider.Error{Kind: provider.KindCancelled, Msg: "untis did not answer in time", Err: err}
		}
		return res, &provider.Error{Kind: provider.KindInternal, Msg: "could not run untis", Err: err}
	}
	if res.ExitCode != 0 {
		return res, exitError(res)
	}
	return res, nil
}

func exitError(res Result) error {
	msg := lastLine(res.Stderr)
	if msg == "" {
		msg = "untis exited with code " + strconv.Itoa(res.ExitCode)
	}
	kind := provider.KindInternal
	switch res.ExitCode {
	case exitConfig:
		kind = provider.KindConfig
	case exitLogin:
		kind = provider.KindLogin
	case exitUnreachable:
		kind = provider.KindUnreachable
	case exitNoSchool:
		kind = provider.KindNoSchool
	case exitNotFound:
		kind = provider.KindNotFound
	case exitInterrupted:
		kind = provider.KindCancelled
	}
	return &provider.Error{Kind: kind, Msg: msg}
}

// lastLine returns the last non-empty stderr line without the "untis: "
// prefix the CLI puts on its error messages.
func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return strings.TrimPrefix(l, "untis: ")
		}
	}
	return ""
}

var whoLine = regexp.MustCompile(`^(\S+)\s{2,}(subject|teacher|room|class)\s{2,}(.*)$`)

// ParseWho parses the `CODE   kind   Name` lines of `untis --who`.
func ParseWho(out []byte) []domain.Lookup {
	var res []domain.Lookup
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := whoLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		res = append(res, domain.Lookup{Code: m[1], Kind: m[2], Name: strings.TrimSpace(m[3])})
	}
	return res
}
