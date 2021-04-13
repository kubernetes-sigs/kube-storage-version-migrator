package tests

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	migrationv1alpha1 "sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/version"
	"sigs.k8s.io/kube-storage-version-migrator/test/e2e/chaosmonkey"
	"sigs.k8s.io/kube-storage-version-migrator/test/e2e/util"
)

const (
	crdGroup    = "migrationtest.k8s.io"
	crdVersion  = "v1"
	crdKind     = "Test"
	crdResource = "tests"
	namespace   = "chaos-test"
	numberOfCRs = 100
)

var (
	v1Hash string
	v2Hash string
)

// StorageMigratorChaosTest verifies that the migrator works under chaotic
// conditions.
type StorageMigratorChaosTest struct {
	migrationClient *migrationclient.Clientset
	crClient        dynamic.ResourceInterface
	kubeClient      *kubernetes.Clientset
}

func (t *StorageMigratorChaosTest) crCreation() {
	ctx := context.TODO()
	_, err := t.kubeClient.CoreV1().Namespaces().Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		util.Failf("failed to create namespace: %v", err)
	}
	for i := 0; i < numberOfCRs; i++ {
		crInstanceName := fmt.Sprintf("cr-instance-%d", i)
		crInstance := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"kind":       crdKind,
				"apiVersion": crdGroup + "/" + "v1",
				"metadata": map[string]interface{}{
					"name":      crInstanceName,
					"namespace": namespace,
				},
				"data": map[string]interface{}{
					"hello": "this is a cr",
				},
			},
		}
		_, err := t.crClient.Create(ctx, crInstance, metav1.CreateOptions{})
		if err != nil {
			util.Failf("failed to create CR: %v", err)
		}
	}
}

func (t *StorageMigratorChaosTest) setupClients() {
	cfg, err := clientcmd.BuildConfigFromFlags("", "/workspace/.kube/config")
	if err != nil {
		util.Failf("can't build client config: %v", err)
	}
	cfg.UserAgent = "storage-migration-chaos-test/" + version.VERSION
	t.migrationClient, err = migrationclient.NewForConfig(cfg)
	if err != nil {
		util.Failf("can't build migration client: %v", err)
	}
	t.kubeClient, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		util.Failf("can't build kubernetes client: %v", err)
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		util.Failf("failed to initialize dynamic client: %v", err)
	}
	gvr := schema.GroupVersionResource{Group: crdGroup, Version: "v1", Resource: crdResource}
	t.crClient = dynamicClient.Resource(gvr).Namespace(namespace)
}

func (t *StorageMigratorChaosTest) Setup() {
	ctx := context.TODO()
	setupMigrator()
	t.setupClients()

	By("Wait for the testCRD to appear in the discovery document")
	err := wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
		var err error
		v1Hash, err = storageVersionHash(t.migrationClient.Discovery(), crdGroup+"/"+crdVersion, crdResource)
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

	By("Create the custom resources")
	t.crCreation()

	By("Wait for the storage state of the CRD to be created")
	var crdStorageState *migrationv1alpha1.StorageState
	err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
		var err error
		crdStorageState, err = t.migrationClient.MigrationV1alpha1().StorageStates().Get(ctx, "tests.migrationtest.k8s.io", metav1.GetOptions{})
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
		util.Failf("unexpected storage version hash %s, expected %s", a, e)
	}

	By("Change the storage version of the CRD")
	output, err := exec.Command("kubectl", "patch", "crd", "tests.migrationtest.k8s.io", `--patch={"spec":{"versions":[{"name":"v1","served":true,"storage":false},{"name":"v2","served":true,"storage":true}]}}`).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}

	By("Wait for the storageVersionHash of the CRD to change in the discovery document")
	err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
		v2Hash, err = storageVersionHash(t.migrationClient.Discovery(), crdGroup+"/"+crdVersion, crdResource)
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
}

func (t *StorageMigratorChaosTest) Test(done <-chan struct{}) {
	ctx := context.TODO()
	// Block until disruptions is done
	<-done
	By("Wait for the apiserver to come back")
	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		healthStatus := 0
		t.migrationClient.Discovery().RESTClient().Get().AbsPath("/healthz").Do(ctx).StatusCode(&healthStatus)
		if healthStatus != http.StatusOK {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		util.Failf("timed out waiting for apiserver to come back: %v", err)
	}

	// Verify the migrations can complete
	By("Wait for the storage state of the CRD to change")
	util.Logf("v1Hash is %s", v1Hash)
	util.Logf("v2Hash is %s", v2Hash)

	// Wait for discoveryPeriod + 1 minute to give the triggering controller enough time to detect and react.
	err = wait.PollImmediate(10*time.Second, discoveryPeriod+1*time.Minute, func() (bool, error) {
		crdStorageState, err := t.migrationClient.MigrationV1alpha1().StorageStates().Get(ctx, "tests.migrationtest.k8s.io", metav1.GetOptions{})
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
		l, err := t.migrationClient.MigrationV1alpha1().StorageStates().List(ctx, metav1.ListOptions{})
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
	l, err := t.migrationClient.MigrationV1alpha1().StorageVersionMigrations().List(ctx, metav1.ListOptions{})
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
}

func (t *StorageMigratorChaosTest) Run(sem *chaosmonkey.Semaphore) {
	var once sync.Once
	ready := func() {
		once.Do(func() {
			sem.Ready()
		})
	}
	defer ready()

	t.Setup()
	ready()
	t.Test(sem.StopCh)
}

func apiserverAndControllerRestartsFunc() {
	apiserverRestartsFunc()
	controllerRestartsFunc()
}

func apiserverRestartsFunc() {
	args := []string{"compute", "--project", os.Getenv("PROJECT"), "ssh", "--zone", os.Getenv("KUBE_GCE_ZONE"), os.Getenv("CLUSTER_NAME") + "-master", "--command", "sudo pkill -9 kube-apiserver"}

	start := time.Now()
	// Continuously restarting the apiserver.
	for time.Now().Before(start.Add(6 * time.Minute)) {
		By("pkill the apiserver")
		output, err := exec.Command("gcloud", args...).CombinedOutput()
		if err != nil {
			util.Failf("%s", output)
		}

		By("Wait for the apiserver to come back")
		// Kubelet restarts static pod with exponential back off, which
		// "capped at five minutes, and is reset after ten minutes of
		// successful execution".
		err = wait.PollImmediate(10*time.Second, 6*time.Minute, func() (bool, error) {
			output, err := exec.Command("kubectl", "get", "--raw=/readyz").CombinedOutput()
			if string(output) != "ok" {
				util.Logf("apiserver not ready yet, output=%q, err=%v", output, err)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			util.Failf("failed waiting for apiserver to come back: %v", err)
		}
	}
}

func controllerRestartsFunc() {
	By("delete and then recreate trigger and migrator")
	trigger := "../../manifests.local/trigger.yaml"
	migrator := "../../manifests.local/migrator.yaml"
	output, err := exec.Command("kubectl", "delete", "-f", trigger, "--wait=true").CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "delete", "-f", migrator, "--wait=true").CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "apply", "-f", migrator).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "apply", "-f", trigger).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
}

var _ = Describe("[Disruptive] storage version migrator", func() {
	It("should survive apiserver crashes and controller restarts", func() {
		cm := chaosmonkey.New(apiserverAndControllerRestartsFunc)
		t := &StorageMigratorChaosTest{}
		cm.Register(t.Run)
		cm.Do()
	})
})
