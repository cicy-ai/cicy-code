package main

import (
	"os"

	"github.com/cicy-ai/cicy-skills/internal/hosttools"
)

func main() {
	os.Exit(hosttools.Run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr))
}
