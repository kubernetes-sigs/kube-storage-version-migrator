package tests

import (
	"context"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	migrationv1alpha1 "sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/version"
	"sigs.k8s.io/kube-storage-version-migrator/test/e2e/util"
)

const (
	// The migration trigger controller redo the discovery every discoveryPeriod.
	discoveryPeriod = 10 * time.Minute
)

func succeeded(conditions []migrationv1alpha1.MigrationCondition) bool {
	for _, c := range conditions {
		if c.Type == migrationv1alpha1.MigrationSucceeded && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func storageVersionHash(d discovery.DiscoveryInterface, groupversion, resource string) (string, error) {
	rl, err := d.ServerResourcesForGroupVersion(groupversion)
	if err != nil {
		return "", err
	}
	for _, r := range rl.APIResources {
		if r.Name == resource {
			return r.StorageVersionHash, nil
		}
	}
	return "", nil
}

var _ = Describe("storage version migrator", func() {
	It("should migrate CRD", func() {
		ctx := context.TODO()
		setupMigrator()
		cfg, err := clientcmd.BuildConfigFromFlags("", "/workspace/.kube/config")
		if err != nil {
			util.Failf("can't build client config: %v", err)
		}
		cfg.UserAgent = "storage-migration-e2e-test/" + version.VERSION
		client, err := migrationclient.NewForConfig(cfg)
		if err != nil {
			util.Failf("can't build client: %v", err)
		}

		By("Wait for the testCRD to appear in the discovery document")
		var v1Hash string
		err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
			v1Hash, err = storageVersionHash(client.Discovery(), "migrationtest.k8s.io/v1", "tests")
			if err != nil {
				util.Logf("failed to fetch the storage version of the crd, %v. Retrying.", err)
				return false, nil
			}
			if v1Hash == "" {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			util.Failf("%v", err)
		}
		By("Wait for storage states to be created")
		err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
			l, err := client.MigrationV1alpha1().StorageStates().List(ctx, metav1.ListOptions{})
			if err != nil {
				util.Failf("%v", err)
			}
			if len(l.Items) != 0 {
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			util.Failf("%v", err)
		}

		By("Wait for the storage state of the CRD to be created")
		var crdStorageState *migrationv1alpha1.StorageState
		err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
			var err error
			crdStorageState, err = client.MigrationV1alpha1().StorageStates().Get(ctx, "tests.migrationtest.k8s.io", metav1.GetOptions{})
			if err != nil && !errors.IsNotFound(err) {
				util.Failf("%v", err)
			}
			if err != nil && errors.IsNotFound(err) {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			util.Failf("%v", err)
		}

		if a, e := crdStorageState.Status.CurrentStorageVersionHash, v1Hash; a != e {
			util.Failf("unexpected storage version hash %s, expected, %s", a, e)
		}

		By("Change the storage version of the CRD")
		output, err := exec.Command("kubectl", "patch", "crd", "tests.migrationtest.k8s.io", `--patch={"spec":{"versions":[{"name":"v1","served":true,"storage":false},{"name":"v2","served":true,"storage":true}]}}`).CombinedOutput()
		if err != nil {
			util.Failf("%s", output)
		}

		By("Wait for the storageVersionHash of the CRD to change in the discovery document")
		var v2Hash string
		err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
			v2Hash, err = storageVersionHash(client.Discovery(), "migrationtest.k8s.io/v1", "tests")
			if err != nil {
				util.Logf("failed to fetch the storage version of the crd, %v. Retrying.", err)
				return false, nil
			}
			if v2Hash == "" || v2Hash == v1Hash {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			util.Failf("%v", err)
		}

		By("Wait for the storage state of the CRD to change")
		// Wait for discoveryPeriod + 1 minute to give the triggering controller enough time to detect and react.
		err = wait.PollImmediate(10*time.Second, discoveryPeriod+1*time.Minute, func() (bool, error) {
			var err error
			crdStorageState, err = client.MigrationV1alpha1().StorageStates().Get(ctx, "tests.migrationtest.k8s.io", metav1.GetOptions{})
			if err != nil {
				util.Failf("%v", err)
			}
			if crdStorageState.Status.CurrentStorageVersionHash != v2Hash {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			util.Failf("%v", err)
		}

		By("Wait for all storage states to converge")
		err = wait.PollImmediate(30*time.Second, 10*time.Minute, func() (bool, error) {
			l, err := client.MigrationV1alpha1().StorageStates().List(ctx, metav1.ListOptions{})
			if err != nil {
				util.Failf("%v", err)
			}
			for _, ss := range l.Items {
				if len(ss.Status.PersistedStorageVersionHashes) == 1 && ss.Status.PersistedStorageVersionHashes[0] == ss.Status.CurrentStorageVersionHash {
					continue
				}
				util.Logf("resource %v has persisted hashes %v, and current hash %s",
					ss.Spec.Resource,
					ss.Status.PersistedStorageVersionHashes,
					ss.Status.CurrentStorageVersionHash)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			util.Failf("%v", err)
		}

		By("Migrations should have all completed")
		l, err := client.MigrationV1alpha1().StorageVersionMigrations().List(ctx, metav1.ListOptions{})
		if err != nil {
			util.Failf("%v", err)
		}
		for _, m := range l.Items {
			if !succeeded(m.Status.Conditions) {
				util.Failf("unexpected in progress migration for resource %v", m.Spec.Resource)
			}
		}
		// TODO: actually verify the etcd contents:
		// kubectl log into the etcd pod
		// etcd pod name: etcd-server-bootstrap-e2e-master
		// namespace: kube-system
	})
})
