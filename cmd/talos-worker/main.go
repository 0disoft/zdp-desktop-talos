package main

import (
	"fmt"
	"os"

	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

func main() {
	if err := workeripc.Serve(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "talos-worker: %v\n", err)
		os.Exit(1)
	}
}
