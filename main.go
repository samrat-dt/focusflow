package main

import (
	"os"

	"focusflow/internal/app"
)

// version is the binary version string. Overridden at build time via
// `-ldflags -X main.version=<tag>` from the GitHub Actions release
// workflow. A value of "dev" means an unversioned local build.
var version = "dev"

func main() {
	app.Version = version
	os.Exit(app.Run(os.Args[1:]))
}
