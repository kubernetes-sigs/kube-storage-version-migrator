package app

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	migrationclient "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clients/clientset"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/controller"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	migratorUserAgent = "storage-version-migration-migrator"
)

var (
	kubeconfigPath = flag.String("kubeconfig", "", "absolute path to the kubeconfig file specifying the apiserver instance. If unspecified, fallback to in-cluster configuration")
)

func NewMigratorCommand() *cobra.Command {
	return &cobra.Command{
		Use:  "kube-storage-migrator",
		Long: `The Kubernetes storage migrator migrates resources based on the StorageVersionMigrations APIs.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := Run(wait.NeverStop); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}
}

func Run(stopCh <-chan struct{}) error {
	http.Handle("/metrics", promhttp.Handler())
	go func() { http.ListenAndServe(":2112", nil) }()

	var err error
	var config *rest.Config
	if *kubeconfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfigPath)
		if err != nil {
			log.Fatalf("Error initializing client config: %v for kubeconfig: %v", err.Error(), *kubeconfigPath)
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return err
		}
	}
	config.UserAgent = migratorUserAgent + "/" + version.VERSION
	dynamic, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	migration, err := migrationclient.NewForConfig(config)
	if err != nil {
		return err
	}
	c := controller.NewKubeMigrator(
		dynamic,
		migration,
	)
	c.Run(stopCh)
	panic("unreachable")
}
