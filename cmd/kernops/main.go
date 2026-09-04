package main

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/cockpit"
)

func main() {
	os.Exit(cockpit.RunOpsCLI(os.Args[1:], os.Stdout, os.Stderr))
}
