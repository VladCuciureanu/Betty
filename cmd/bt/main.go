package main

import (
	"os"

	"github.com/vladcuciureanu/betty/internal/app"
	"github.com/vladcuciureanu/betty/internal/hidbattery"
)

var version = "dev"

func main() {
	if hidbattery.ServeHelper(os.Args[1:], os.Stdout) {
		return
	}
	os.Exit(app.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version))
}
