package main

import (
	goflag "flag"
	"fmt"
	"os"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
	"sigs.k8s.io/kube-storage-version-migrator/cmd/trigger/app"
)

func main() {
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	pflag.Parse()
	pflag.VisitAll(func(flag *pflag.Flag) {
		klog.V(4).Infof("FLAG: --%s=%q", flag.Name, flag.Value)
		fmt.Printf("FLAG: --%s=%q\n", flag.Name, flag.Value)
	})
	command := app.NewTriggerCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
