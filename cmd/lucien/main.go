// Command lucien is the notes backend: a self-hosted REST service that
// stores notes as git-versioned JSON files and processes them through
// asynchronous pipelines. See docs/DESIGN.md.
package main

import (
	"fmt"
	"os"
)

const version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "lucien %s starting\n", version)
	// Slices land here: config load, server boot, worker loop.
}
