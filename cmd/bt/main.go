package main

import (
	"os"

	"github.com/vladcuciureanu/betty/internal/app"
)

var version = "dev"

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version))
}
