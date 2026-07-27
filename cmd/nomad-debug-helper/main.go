package main

import (
	"os"

	"github.com/louie/nomad-debug-helper/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:]))
}
