package app

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/controller"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/version"
)

const (
	migratorUserAgent = "storage-version-migration-migrator"
)

var (
	kubeconfigPath        = flag.String("kubeconfig", "", "absolute path to the kubeconfig file specifying the apiserver instance. If unspecified, fallback to in-cluster configuration")
	kubeAPIQPS            = flag.Float32("kube-api-qps", rest.DefaultQPS, "QPS to use while talking with kubernetes apiserver.")
	kubeAPIBurst          = flag.Int("kube-api-burst", rest.DefaultBurst, "Burst to use while talking with kubernetes apiserver.")
	resourceLockNamespace = flag.String("resource-lock-ns", getDefaultResourceLockNamespace(), "Namespace to create leader election lock resource in.")
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
	livenessHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})
	http.HandleFunc("/healthz", livenessHandler)
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

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	leaderElectionConfig := newLeaderElectionConfig(kubeClient.CoreV1())

	leaderElectionConfig.Callbacks.OnStartedLeading = func(ctx context.Context) {
		c.Run(ctx)
	}

	leaderelection.RunOrDie(ctx, *leaderElectionConfig)

	panic("unreachable")

}

func getDefaultResourceLockNamespace() string {
	if data, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if ns := strings.TrimSpace(string(data)); len(ns) > 0 {
			return ns
		}
	}
	return "default"
}

func newLeaderElectionConfig(client v1.ConfigMapsGetter) *leaderelection.LeaderElectionConfig {

	id := string(uuid.NewUUID())
	if hostname, err := os.Hostname(); err == nil {
		id = hostname + "_" + id
	}

	if len(*resourceLockNamespace) == 0 {
	}

	lock := &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Name:      "migrator-lock",
			Namespace: *resourceLockNamespace,
		},
		Client: client,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}

	return &leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: 60 * time.Second,
		RenewDeadline: 35 * time.Second,
		RetryPeriod:   10 * time.Second,
		Name:          "",
		Callbacks: leaderelection.LeaderCallbacks{
			OnStoppedLeading: func() {
				defer os.Exit(0)
				klog.Warningf("leader election lost")
			},
		},
	}
}
