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

func parseArgs() (*tfquiet.Options, string) {
	var CLI struct {
		tfquiet.Options
		File    string `arg:"" optional:"" help:"Terraform plan output file. If not specified, read from stdin."`
		Version kong.VersionFlag
	}

	parser := kong.Must(&CLI, kong.Vars{"version": version})
	parser.Model.HelpFlag.Help = "Show help."

	if _, err := parser.Parse(os.Args[1:]); err != nil {
		parser.FatalIfErrorf(err)
	}

	return &CLI.Options, CLI.File
}

func main() {
	opts, file := parseArgs()

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

	out, err := tfquiet.Filter(r, opts)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(string(out))
}
