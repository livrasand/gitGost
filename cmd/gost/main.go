package main

import (
	"os"

	"github.com/livrasand/gitGost/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
