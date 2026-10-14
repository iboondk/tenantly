// Command k6report turns a k6 summary export into the Markdown results table.
//
//	k6report -in loadtest/results/summary.json
//
// The table is printed to stdout. Cells the input does not contain show "n/a".
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/iboondk/tenantly/internal/k6report"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "k6report: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("k6report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	in := fs.String("in", "", "path to a k6 summary export (summary.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("missing -in; usage: k6report -in loadtest/results/summary.json")
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()

	s, err := k6report.Parse(f)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, k6report.RenderMarkdown(s))
	return err
}
