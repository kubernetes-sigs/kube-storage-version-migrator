package main

import (
	"fmt"
	"os"

	"github.com/kubernetes-sigs/kube-storage-version-migrator/cmd/migrator/app"
)

func main() {
	command := app.NewInitializerCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
