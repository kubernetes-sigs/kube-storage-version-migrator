package main

import (
	goflag "flag"
	"fmt"
	"os"

	"github.com/kubernetes-sigs/kube-storage-version-migrator/cmd/initializer/app"
	"github.com/spf13/pflag"
	"k8s.io/klog"
	"k8s.io/klog/glog"
)

func main() {
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	pflag.Parse()
	pflag.VisitAll(func(flag *pflag.Flag) {
		glog.V(4).Infof("FLAG: --%s=%q", flag.Name, flag.Value)
	})
	command := app.NewInitializerCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
