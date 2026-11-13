// Command moldloft generates a static tool repository from one build
// specification.
//
//	moldloft generate -spec spec.json -out folder
//	moldloft version
//
// Exit status: 0 the repository was written; 1 the specification was refused
// (the error line starts with E_PARSE or E_NEEDS_SUBTOOL and nothing is
// written); 2 the command could not run (bad arguments, unreadable file,
// output folder not empty).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/generate"
	"github.com/ClearedStack/moldloft/internal/repotree"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  moldloft generate -spec <file> -out <folder>
  moldloft version
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, generate.Name, generate.Version)
		return 0
	}
	if len(args) == 0 || args[0] != "generate" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	specPath := flags.String("spec", "", "")
	outDir := flags.String("out", "", "")
	if err := flags.Parse(args[1:]); err != nil || *specPath == "" || *outDir == "" || flags.NArg() != 0 {
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
