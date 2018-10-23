package app

import (
	"fmt"
	"os"

	migrationclient "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clientset"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/controller"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	migratorUserAgent = "storage-version-migration-migrator"
)

func NewInitializerCommand() *cobra.Command {
	return &cobra.Command{
		Use:  "kube-storage-migrator-initializer",
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
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	dynamic, err := dynamic.NewForConfig(rest.AddUserAgent(config, migratorUserAgent))
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
