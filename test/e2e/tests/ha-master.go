package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	apiserverinternalv1alpha1 "k8s.io/api/apiserverinternal/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	migrationv1alpha1 "sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/version"
	"sigs.k8s.io/kube-storage-version-migrator/test/e2e/util"
)

const (
	// The storage version manager self-corrects StorageVersoin objects every 10 minutes
	storageVersionManagerRefreshPeriod = 10 * time.Minute
)

var v1Version string

var _ = Describe("[HAMaster] storage version migrator", func() {
	It("should migrate built-in resource when one server self-corrects corrupted StorageVersion object", func() {
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
		kubeClient := kubernetes.NewForConfigOrDie(cfg)

		By("Wait for the StorageVersion object to appear")
		var sv *apiserverinternalv1alpha1.StorageVersion
		var lastErr error
		err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
			var getErr error
			sv, getErr = kubeClient.InternalV1alpha1().StorageVersions().Get(ctx, "core.configmaps", metav1.GetOptions{})
			if getErr != nil && errors.IsNotFound(getErr) {
				return false, nil
			}
			if getErr != nil {
				return false, getErr
			}
			if len(sv.Status.StorageVersions) != 3 {
				lastErr = fmt.Errorf("expected 3 records, got: %v", sv)
				return false, nil
			}
			if sv.Status.CommonEncodingVersion == nil {
				lastErr = fmt.Errorf("expected a common encoding version, got: %v", sv)
				return false, nil
			}
			v1Version = *sv.Status.CommonEncodingVersion
			return true, nil
		})
		if err != nil {
			util.Failf("failed to wait for StorageVersion object to appear: %v, last error: %v", err, lastErr)
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

		By("Wait for the storage state of the built-in resource to be created")
		var configmapStorageState *migrationv1alpha1.StorageState
		err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
			var err error
			configmapStorageState, err = client.MigrationV1alpha1().StorageStates().Get(ctx, "configmaps", metav1.GetOptions{})
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

		if a, e := configmapStorageState.Status.CurrentStorageVersionHash, v1Version; a != e {
			util.Failf("unexpected storage version hash %s, expected, %s", a, e)
		}

		By("Corrupt the StorageVersion object for one server")
		sv.Status.StorageVersions[0].EncodingVersion = "v99"
		sv.Status.StorageVersions[0].DecodableVersions = append(sv.Status.StorageVersions[0].DecodableVersions, "v99")
		_, err = kubeClient.InternalV1alpha1().StorageVersions().Update(ctx, sv, metav1.UpdateOptions{})
		if err != nil {
			util.Failf("%v", err)
		}

		By("Wait for the storage state of the built-in resource to change")
		// Wait for storageVersionManagerRefreshPeriod + 1 minute to give the triggering controller enough time to detect and react.
		err = wait.PollImmediate(10*time.Second, storageVersionManagerRefreshPeriod+1*time.Minute, func() (bool, error) {
			var err error
			configmapStorageState, err = client.MigrationV1alpha1().StorageStates().Get(ctx, "configmaps", metav1.GetOptions{})
			if err != nil {
				util.Failf("%v", err)
			}
			if configmapStorageState.Status.CurrentStorageVersionHash != v1Version {
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
	})
})
