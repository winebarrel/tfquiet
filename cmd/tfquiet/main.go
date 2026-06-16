package main

import (
	"io"
	"log"
	"os"

	"github.com/alecthomas/kong"
	"github.com/winebarrel/tfquiet"
	"golang.org/x/term"
)

var version string

func init() {
	log.SetFlags(0)
}

type cliArgs struct {
	tfquiet.Options
	File       string `arg:"" optional:"" help:"Terraform plan output file. If not specified, read from stdin."`
	NoProgress bool   `name:"no-progress" help:"Disable the progress meter on stderr."`
	Version    kong.VersionFlag
}

func parseArgs() (*tfquiet.Options, string, bool) {
	var cli cliArgs

	parser := kong.Must(&cli, kong.Vars{"version": version})
	parser.Model.HelpFlag.Help = "Show help."

	if _, err := parser.Parse(os.Args[1:]); err != nil {
		parser.FatalIfErrorf(err)
	}

	return &cli.Options, cli.File, cli.NoProgress
}

func main() {
	opts, file, noProgress := parseArgs()

	if !noProgress && term.IsTerminal(int(os.Stderr.Fd())) {
		opts.Progress = os.Stderr
	}

	var r io.Reader
	if file == "" || file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		r = f
	}

	if err := tfquiet.Filter(r, os.Stdout, opts); err != nil {
		log.Fatal(err)
	}
}
