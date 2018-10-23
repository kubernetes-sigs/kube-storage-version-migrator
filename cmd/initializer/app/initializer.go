package app

import (
	"fmt"
	"os"

	migrationclient "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clientset"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/initializer"
	"github.com/spf13/cobra"
	crdclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	apiserviceclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

const (
	initializerUserAgent = "storage-version-migration-initializer"
)

func NewInitializerCommand() *cobra.Command {
	return &cobra.Command{
		Use:  "kube-storage-migrator-initializer",
		Long: `The Kubernetes storage migrator initializer is a job that discovers resources that need migration and creates storageVersionMigration objects for such resources.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := Run(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}
}

func Run() error {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(rest.AddUserAgent(config, initializerUserAgent))
	if err != nil {
		return err
	}
	crd, err := crdclient.NewForConfig(config)
	if err != nil {
		return err
	}
	apiservice, err := apiserviceclient.NewForConfig(config)
	if err != nil {
		return err
	}
	migration, err := migrationclient.NewForConfig(config)
	if err != nil {
		return err
	}
	init := initializer.NewInitializer(
		clientset.Discovery(),
		crd.ApiextensionsV1beta1().CustomResourceDefinitions(),
		apiservice.ApiregistrationV1().APIServices(),
		clientset.CoreV1().Namespaces(),
		migration.MigrationV1alpha1(),
	)
	return init.Initialize()
}
