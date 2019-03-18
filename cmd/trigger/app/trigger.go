package app

import (
	"fmt"
	"os"

	migrationclient "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clients/clientset"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/trigger"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	migratorUserAgent = "storage-version-migration-migrator"
)

func NewTriggerCommand() *cobra.Command {
	return &cobra.Command{
		Use: "kube-storage-migrator-trigger",
		Long: `The Kubernetes storage migrator triggering controller
		detects storage version changes and creates migration requests.
		It also records the status of the storage via the storageState
		API.`,
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
	migration, err := migrationclient.NewForConfig(rest.AddUserAgent(config, migratorUserAgent))
	if err != nil {
		return err
	}
	c := trigger.NewMigrationTrigger(migration)
	c.Run(stopCh)
	panic("unreachable")
}
