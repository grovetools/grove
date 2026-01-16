package main

import (
	"os"

	"github.com/grovetools/grove/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
