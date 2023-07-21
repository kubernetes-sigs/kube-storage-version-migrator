package app

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/cli/flag"

	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/trigger"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/version"
)

const (
	triggerUserAgent = "storage-version-migration-trigger"
)

var (
	kubeconfigPath = pflag.String("kubeconfig", "", "absolute path to the kubeconfig file specifying the apiserver instance. If unspecified, fallback to in-cluster configuration")
)

func NewTriggerCommand(ctx context.Context) *cobra.Command {
	c := &cobra.Command{
		Use: "kube-storage-migrator-trigger",
		Long: `The Kubernetes storage migrator triggering controller
		detects storage version changes and creates migration requests.
		It also records the status of the storage via the storageState
		API.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flag.PrintFlags(cmd.Flags())
			return run(cmd.Context())
		},
	}
	c.SetContext(ctx)
	return c
}

func run(ctx context.Context) error {
	livenessHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})
	http.HandleFunc("/healthz", livenessHandler)
	go func() { http.ListenAndServe(":2113", nil) }()

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
	config.UserAgent = triggerUserAgent + "/" + version.VERSION
	migration, err := migrationclient.NewForConfig(config)
	if err != nil {
		return err
	}
	c := trigger.NewMigrationTrigger(migration)
	c.Run(ctx)
	panic("unreachable")
}
