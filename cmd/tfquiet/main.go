package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/alecthomas/kong"
	"github.com/winebarrel/tfquiet"
)

var version string

func init() {
	log.SetFlags(0)
}

type options struct {
	File        string `arg:"" optional:"" help:"Terraform plan output file. If not specified, read from stdin."`
	ShowMoved   bool   `help:"Show moved blocks."`
	ShowImport  bool   `help:"Show import blocks."`
	ShowRemoved bool   `help:"Show removed{} lifecycle.destroy=false (state-only forget) blocks."`
	ShowNoise   bool   `help:"Show refresh/lock lines and the trailing Note footer."`
	Version     kong.VersionFlag
}

func parseArgs() *options {
	opts := &options{}
	parser := kong.Must(opts, kong.Vars{"version": version})
	parser.Model.HelpFlag.Help = "Show help."

	if _, err := parser.Parse(os.Args[1:]); err != nil {
		parser.FatalIfErrorf(err)
	}

	return opts
}

func main() {
	opts := parseArgs()

	var r io.Reader
	if opts.File == "" || opts.File == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(opts.File)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		r = f
	}

	out, err := tfquiet.Filter(r,
		tfquiet.OptionShowMoved(opts.ShowMoved),
		tfquiet.OptionShowImport(opts.ShowImport),
		tfquiet.OptionShowRemoved(opts.ShowRemoved),
		tfquiet.OptionShowNoise(opts.ShowNoise),
	)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(string(out))
}
