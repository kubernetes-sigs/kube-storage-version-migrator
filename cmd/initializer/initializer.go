package main

import (
	"context"
	"os"
	"os/signal"

	"k8s.io/component-base/cli"
	"sigs.k8s.io/kube-storage-version-migrator/cmd/initializer/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(app.NewInitializerCommand(ctx)))
}
