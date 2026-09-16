package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/config"
	"github.com/rvenutolo/github-repos-audit/internal/render"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

const usageText = `audit — report what each repository on this account is missing.

Usage:
  audit <command> [flags]

Commands:
  render     read GitHub, then rewrite README.md and audit.json
  json       read GitHub, then print the snapshot to stdout
  validate   check repos.toml alone; offline, needs no token

render writes both files whole, through a temp file and a rename, only after
every repository has been read successfully — no failure leaves a partial
report behind. It prints one line to stdout,

  material-change=true

or false, saying whether anything a reader would notice differs from what is
already committed. The refresh workflow appends that line to $GITHUB_OUTPUT
and opens a pull request only when it is true.

Flags:
  -h, --help      print this message
      --version   print the version

Subcommand flags:
      --config    path to the configuration file (default repos.toml)
      --dir       render only: directory holding README.md and audit.json
                  (default .)
      --verbose   render and json: log progress to stderr at info level
      --debug     render and json: log everything to stderr at debug level;
                  wins over --verbose

Logging goes to stderr at warn level by default, so a caller that captures
stdout sees nothing else unless something is wrong.

Environment:
  GITHUB_TOKEN    the token used for every read. When unset, render and json
                  fall back to ` + "`gh auth token`" + `. validate needs neither.

Exit codes:
  0   success
  1   runtime failure — an unexpected API response, a coverage mismatch, or a
      malformed repos.toml. The invocation was fine; something else was not.
  2   usage error — an unknown subcommand, a flag that does not parse, or a
      missing or invalid argument.
  130 interrupted by SIGINT or SIGTERM.
`

// errUsage marks a caller mistake — an unknown subcommand, a flag that does
// not parse, an argument that is missing or wrong. It exits 2. A well-formed
// invocation over bad data is a runtime error and exits 1.
var errUsage = errors.New("usage")

// run holds every decision the program makes. It touches no os global, so it
// is driven directly from tests with buffers and a map-backed getenv.
func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	_ io.Reader,
	stdout, stderr io.Writer,
) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return fmt.Errorf("%w: no command given", errUsage)
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageText)
		return nil
	case "--version", "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "render":
		return runRender(ctx, args[1:], getenv, stdout, stderr, liveCollector, time.Now)
	case "json":
		return runJSON(ctx, args[1:], getenv, stdout, stderr, liveCollector, time.Now)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, usageText)
		return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}
}

// newFlagSet builds a subcommand's flag set. ContinueOnError plus an explicit
// output writer keeps a parse failure a returned error rather than a call to
// os.Exit inside the flag package.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("audit "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parse runs fs over args, translating flag's two special outcomes: -h is not
// an error, and anything else that fails to parse is a usage error.
func parse(fs *flag.FlagSet, args []string, stdout io.Writer) (helped bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(stdout)
			fs.Usage()
			return true, nil
		}
		return false, fmt.Errorf("%w: %w", errUsage, err)
	}
	return false, nil
}

// logLevel registers --verbose and --debug on fs and returns the level they
// select, readable once fs is parsed. --debug wins when both are given, so a
// script that always passes --verbose can still be turned up for a diagnosis.
func logLevel(fs *flag.FlagSet) func() slog.Level {
	verbose := fs.Bool("verbose", false, "log progress at info level")
	debug := fs.Bool("debug", false, "log everything at debug level; wins over --verbose")
	return func() slog.Level {
		switch {
		case *debug:
			return slog.LevelDebug
		case *verbose:
			return slog.LevelInfo
		default:
			return slog.LevelWarn
		}
	}
}

func runValidate(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("validate", stderr)
	path := fs.String("config", "repos.toml", "path to the configuration file")
	if helped, err := parse(fs, args, stdout); helped || err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: validate takes no arguments, got %q", errUsage, fs.Arg(0))
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	// The parser checks what it can see; the rules check what only the types table knows,
	// which is whether an override says anything the type does not already.
	if err := rules.ValidateOffline(cfg.Types, cfg.Declarations()); err != nil {
		return fmt.Errorf("%s: %w", *path, err)
	}
	fmt.Fprintf(stdout, "%s: %d repositories, no problems found\n", *path, len(cfg.Repos))
	return nil
}

func runRender(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	newClient newCollector,
	now func() time.Time,
) error {
	fs := newFlagSet("render", stderr)
	path := fs.String("config", "repos.toml", "path to the configuration file")
	dir := fs.String("dir", ".", "directory holding README.md and audit.json")
	level := logLevel(fs)
	if helped, err := parse(fs, args, stdout); helped || err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: render takes no arguments, got %q", errUsage, fs.Arg(0))
	}
	logger := newLogger(stderr, level())
	logger.DebugContext(ctx, "starting", "command", "render")

	snap, err := prepare(ctx, *path, getenv, logger, newClient, now)
	if err != nil {
		return err
	}

	report, err := rules.Evaluate(snap)
	if err != nil {
		return err
	}
	block, err := render.Block(report, now)
	if err != nil {
		return err
	}

	readmePath := filepath.Join(*dir, "README.md")
	jsonPath := filepath.Join(*dir, "audit.json")

	oldREADME, err := os.ReadFile(readmePath) //nolint:gosec // a path built from a flag, not from a request
	if err != nil {
		return fmt.Errorf("read %s: %w", readmePath, err)
	}
	newREADME, err := render.Splice(string(oldREADME), block)
	if err != nil {
		return fmt.Errorf("%s: %w", readmePath, err)
	}

	newJSON, err := render.JSON(snap)
	if err != nil {
		return err
	}
	// An absent audit.json is the first run, not a failure.
	oldJSON, err := os.ReadFile(jsonPath) //nolint:gosec // a path built from a flag, not from a request
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", jsonPath, err)
	}

	changed, err := render.MaterialChange(oldJSON, newJSON, string(oldREADME), newREADME)
	if err != nil {
		return err
	}

	// Both files are written whatever the verdict. The binary reports; the
	// workflow decides.
	if err := render.WriteFiles(readmePath, newREADME, jsonPath, newJSON); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "material-change=%t\n", changed)
	return nil
}

func runJSON(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	newClient newCollector,
	now func() time.Time,
) error {
	fs := newFlagSet("json", stderr)
	path := fs.String("config", "repos.toml", "path to the configuration file")
	level := logLevel(fs)
	if helped, err := parse(fs, args, stdout); helped || err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: json takes no arguments, got %q", errUsage, fs.Arg(0))
	}
	logger := newLogger(stderr, level())
	logger.DebugContext(ctx, "starting", "command", "json")

	snap, err := prepare(ctx, *path, getenv, logger, newClient, now)
	if err != nil {
		return err
	}
	data, err := render.JSON(snap)
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}

// newLogger builds the one logger the program uses, on the injected stderr at
// the level the flags chose. slog.SetDefault is never called: it is
// process-global and would break parallel tests of run.
func newLogger(stderr io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
}
