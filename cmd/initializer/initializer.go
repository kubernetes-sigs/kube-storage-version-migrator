package main

import (
	goflag "flag"
	"fmt"
	"os"

	"github.com/spf13/pflag"
	"k8s.io/klog"
	"sigs.k8s.io/kube-storage-version-migrator/cmd/initializer/app"
)

func main() {
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	pflag.Parse()
	pflag.VisitAll(func(flag *pflag.Flag) {
		klog.V(4).Infof("FLAG: --%s=%q", flag.Name, flag.Value)
	})
	command := app.NewInitializerCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
