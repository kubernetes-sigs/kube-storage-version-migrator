package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/controller"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/version"
)

const (
	migratorUserAgent = "storage-version-migration-migrator"
)

var (
	kubeconfigPath = flag.String("kubeconfig", "", "absolute path to the kubeconfig file specifying the apiserver instance. If unspecified, fallback to in-cluster configuration")
	kubeAPIQPS     = flag.Float32("kube-api-qps", rest.DefaultQPS, "QPS to use while talking with kubernetes apiserver.")
	kubeAPIBurst   = flag.Int("kube-api-burst", rest.DefaultBurst, "Burst to use while talking with kubernetes apiserver.")
)

func NewMigratorCommand() *cobra.Command {
	return &cobra.Command{
		Use:  "kube-storage-migrator",
		Long: `The Kubernetes storage migrator migrates resources based on the StorageVersionMigrations APIs.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := Run(context.TODO()); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}
}

func Run(ctx context.Context) error {
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
	config.QPS = *kubeAPIQPS
	config.Burst = *kubeAPIBurst
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
	c.Run(ctx)
	panic("unreachable")
}
