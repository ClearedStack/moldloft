// Command moldloft either generates a static tool repository from one local
// build specification or runs the cluster worker loop against Muster.
//
//	moldloft run [flags]
//	moldloft generate -spec spec.json -out folder
//	moldloft version
//
// Exit status: 0 success; 1 specification or worker contract refusal; 2 bad
// arguments or local configuration/storage problem.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/generate"
	"github.com/ClearedStack/moldloft/internal/muster"
	"github.com/ClearedStack/moldloft/internal/plainforge"
	"github.com/ClearedStack/moldloft/internal/repotree"
	"github.com/ClearedStack/moldloft/internal/spool"
	"github.com/ClearedStack/moldloft/internal/worker"
)

func main() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(24 << 20)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  moldloft run [-muster <url>] [-plainforge <url>] [-spool <dir>] [-once] [-standalone] [-seed <file>]
  moldloft generate -spec <file> -out <folder>
  moldloft version
`

func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, generate.Name, generate.Version)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "run":
		return runWorker(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}

func runGenerate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	specPath := flags.String("spec", "", "")
	outDir := flags.String("out", "", "")
	if err := flags.Parse(args); err != nil || *specPath == "" || *outDir == "" || flags.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	// Local problems are described in fixed words. An operating-system error
	// is never printed, because it would name paths on this machine.
	raw, err := readSpec(*specPath)
	if err != nil {
		fmt.Fprintln(stderr, "moldloft: "+err.Error())
		return 2
	}
	tree, err := generate.Generate(raw)
	if err != nil {
		if errs.CodeOf(err) == "" {
			fmt.Fprintln(stderr, "moldloft: internal error while generating")
			return 2
		}
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := tree.Write(*outDir); err != nil {
		if errors.Is(err, repotree.ErrNotEmpty) {
			fmt.Fprintln(stderr, "moldloft: "+err.Error())
		} else {
			fmt.Fprintln(stderr, "moldloft: the output folder could not be written")
		}
		return 2
	}
	fmt.Fprintf(stdout, "generated %d files\n", len(tree))
	return 0
}

func runWorker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	musterURL := flags.String("muster", envOr("MUSTER_URL", ""), "")
	plainforgeURL := flags.String("plainforge", envOr("PLAINFORGE_URL", ""), "")
	spoolDir := flags.String("spool", envOr("MOLDLOFT_SPOOL_DIR", "/var/spool/cs"), "")
	tokenFile := flags.String("token-file", envOr("MOLDLOFT_TOKEN_FILE", muster.DefaultTokenFile), "")
	publicBase := flags.String("public-repo-base", envOr("MOLDLOFT_PUBLIC_REPO_BASE", worker.DefaultPublicRepoBase), "")
	pollDur := flags.Duration("poll", envDuration("MOLDLOFT_POLL_INTERVAL", 5*time.Second), "")
	once := flags.Bool("once", false, "")
	standalone := flags.Bool("standalone", false, "")
	seedPath := flags.String("seed", "", "")

	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if cv := envOr("CONTRACT_VERSION", muster.ContractVersion); cv != muster.ContractVersion {
		fmt.Fprintln(stderr, "E_NEEDS_SUBTOOL: CONTRACT_VERSION: unsupported contract version")
		return 1
	}
	if *seedPath != "" && !*standalone {
		fmt.Fprintln(stderr, "moldloft: -seed may only be used with -standalone")
		return 2
	}

	pfUser, pfToken, pfAuth, err := plainforge.LoadCredentials(os.Getenv("MOLDLOFT_PLAINFORGE_CREDENTIALS_DIR"))
	if err != nil {
		fmt.Fprintln(stderr, "moldloft: "+err.Error())
		return 2
	}

	var tokenSource muster.TokenSource
	var cleanup func()
	if *standalone {
		mURL, pURL, closeServers, err := startStandalone(*seedPath)
		if err != nil {
			fmt.Fprintln(stderr, "moldloft: "+err.Error())
			return 2
		}
		cleanup = closeServers
		defer cleanup()
		*musterURL = mURL
		if *plainforgeURL == "" {
			*plainforgeURL = pURL
		}
		tokenSource = muster.StaticTokenSource("moldloft")
	} else {
		if strings.TrimSpace(*musterURL) == "" {
			fmt.Fprintln(stderr, "moldloft: MUSTER_URL or -muster is required unless -standalone is set")
			return 2
		}
		tokenSource = muster.FileTokenSource(*tokenFile)
	}

	mc, err := muster.NewHTTPClient(*musterURL, tokenSource, 15*time.Second)
	if err != nil {
		fmt.Fprintln(stderr, "moldloft: invalid muster configuration")
		return 2
	}

	var committer plainforge.Committer = plainforge.DryCommitter{}
	if strings.TrimSpace(*plainforgeURL) != "" {
		pc, err := plainforge.NewHTTPCommitter(*plainforgeURL, 15*time.Second)
		if err != nil {
			fmt.Fprintln(stderr, "moldloft: invalid plainforge configuration")
			return 2
		}
		if pfAuth {
			pc.SetBasicAuth(pfUser, pfToken)
		}
		committer = pc
	}

	sp, err := spool.Open(*spoolDir)
	if err != nil {
		fmt.Fprintln(stderr, "moldloft: "+err.Error())
		return 2
	}

	w, err := worker.New(worker.Config{
		Muster:         mc,
		Committer:      committer,
		Spool:          sp,
		PublicRepoBase: *publicBase,
		PollInterval:   *pollDur,
		Log:            stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, "moldloft: "+err.Error())
		return 2
	}

	if *once {
		if _, err := w.Drain(ctx); err != nil {
			fmt.Fprintln(stderr, muster.CodeOfError(err))
			return 1
		}
		return 0
	}
	if err := w.Run(ctx); err != nil {
		fmt.Fprintln(stderr, muster.CodeOfError(err))
		return 1
	}
	return 0
}

func startStandalone(seedPath string) (musterURL, plainforgeURL string, cleanup func(), err error) {
	ms := muster.NewStandIn()
	if seedPath != "" {
		raw, readErr := readSeed(seedPath)
		if readErr != nil {
			return "", "", nil, readErr
		}
		var seeds []muster.SeedItem
		if decErr := json.Unmarshal(raw, &seeds); decErr != nil {
			return "", "", nil, errors.New("the seed file is not a valid seed list")
		}
		for _, item := range seeds {
			ms.Seed(item)
		}
	}
	pf := plainforge.NewStandIn()

	mLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, errors.New("standalone muster listener could not start")
	}
	pLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		mLn.Close()
		return "", "", nil, errors.New("standalone plainforge listener could not start")
	}

	mSrv := &http.Server{Handler: ms, ReadHeaderTimeout: 5 * time.Second}
	pSrv := &http.Server{Handler: pf, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = mSrv.Serve(mLn) }()
	go func() { _ = pSrv.Serve(pLn) }()

	closeAll := func() {
		_ = mSrv.Close()
		_ = pSrv.Close()
	}
	return "http://" + mLn.Addr().String(), "http://" + pLn.Addr().String(), closeAll, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func readSpec(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("the spec file does not exist")
	}
	if err != nil {
		return nil, errors.New("the spec file could not be opened")
	}
	defer f.Close()
	// One byte more than the limit is enough for the parser to refuse it.
	raw, err := io.ReadAll(io.LimitReader(f, buildspec.MaxSpecBytes+1))
	if err != nil {
		return nil, errors.New("the spec file could not be read")
	}
	return raw, nil
}

func readSeed(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("the seed file does not exist")
	}
	if err != nil {
		return nil, errors.New("the seed file could not be opened")
	}
	defer f.Close()
	const maxSeedBytes = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(f, maxSeedBytes+1))
	if err != nil || len(raw) > maxSeedBytes {
		return nil, errors.New("the seed file could not be read")
	}
	return raw, nil
}
