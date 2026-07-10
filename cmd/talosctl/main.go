package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/doctor"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "doctor" {
		_, _ = fmt.Fprintln(os.Stderr, "usage: talosctl doctor [--json] [--worker <path>]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "write one versioned JSON report")
	workerPath := flags.String("worker", "", "override the sibling worker path")
	if err := flags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := doctor.Run(ctx, *workerPath)
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "encode doctor report: %v\n", err)
			os.Exit(10)
		}
	} else {
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(os.Stdout, "%-20s %s\n", check.Name, check.Status)
		}
	}
	if !report.Ready {
		os.Exit(3)
	}
}
