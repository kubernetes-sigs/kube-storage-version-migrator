/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package trigger

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset/fake"
)

func TestProcessDiscoveryResource(t *testing.T) {
	// TODO: we probably don't need a list
	client := fake.NewSimpleClientset(newMigrationList())
	trigger := NewMigrationTrigger(client)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	discoveredResource := newAPIResource()
	trigger.processDiscoveryResource(context.TODO(), discoveredResource)
	actions := client.Actions()
	verifyCleanupAndLaunch(t, actions[3:7])

	c, ok := actions[8].(core.CreateAction)
	if !ok {
		t.Fatalf("expected create action")
	}
	r := schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storagestates"}
	if c.GetResource() != r {
		t.Fatalf("unexpected resource %v", c.GetResource())
	}

	verifyStorageStateUpdate(t, actions[9], trigger.heartbeat, discoveredResource.StorageVersionHash, []string{v1alpha1.Unknown})
}

func TestProcessDiscoveryResourceStaleState(t *testing.T) {
	client := fake.NewSimpleClientset(newMigrationList(), storageState(withStaleHeartbeat()))
	trigger := NewMigrationTrigger(client)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	discoveredResource := newAPIResource()
	trigger.processDiscoveryResource(context.TODO(), discoveredResource)

	actions := client.Actions()
	d, ok := actions[3].(core.DeleteAction)
	if !ok {
		t.Fatalf("expected delete action")
	}
	r := schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storagestates"}
	if d.GetResource() != r {
		t.Fatalf("unexpected resource %v", d.GetResource())
	}
	if !strings.Contains(d.GetName(), "pods") {
		t.Fatalf("unexpected name %s", d.GetName())
	}

	verifyCleanupAndLaunch(t, actions[4:8])

	c, ok := actions[9].(core.CreateAction)
	if !ok {
		t.Fatalf("expected create action")
	}
	r = schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storagestates"}
	if c.GetResource() != r {
		t.Fatalf("unexpected resource %v", c.GetResource())
	}

	verifyStorageStateUpdate(t, actions[10], trigger.heartbeat, discoveredResource.StorageVersionHash, []string{v1alpha1.Unknown})
}

func TestProcessDiscoveryResourceStorageVersionChanged(t *testing.T) {
	client := fake.NewSimpleClientset(
		newMigrationList(),
		storageState(
			withFreshHeartbeat(),
			withCurrentVersion("oldhash"),
			withPersistedVersions("oldhash"),
		),
	)
	trigger := NewMigrationTrigger(client)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	discoveredResource := newAPIResource()
	trigger.processDiscoveryResource(context.TODO(), discoveredResource)

	actions := client.Actions()
	verifyCleanupAndLaunch(t, actions[3:7])
	verifyStorageStateUpdate(t, actions[8], trigger.heartbeat, discoveredResource.StorageVersionHash, []string{"oldhash", "newhash"})
}

func TestProcessDiscoveryResourceNoChange(t *testing.T) {
	client := fake.NewSimpleClientset(
		newMigrationList(),
		storageState(
			withFreshHeartbeat(),
			withCurrentVersion("newhash"),
			withPersistedVersions("newhash"),
		),
	)
	trigger := NewMigrationTrigger(client)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	discoveredResource := newAPIResource()
	trigger.processDiscoveryResource(context.TODO(), discoveredResource)

	actions := client.Actions()
	verifyStorageStateUpdate(t, actions[4], trigger.heartbeat, discoveredResource.StorageVersionHash, []string{"newhash"})
}

func TestProcessDiscoveryResourceStorageMigrationMissing(t *testing.T) {
	client := fake.NewSimpleClientset(
		storageState(
			withFreshHeartbeat(),
			withCurrentVersion("newhash"),
			withPersistedVersions(v1alpha1.Unknown),
		),
	)
	trigger := NewMigrationTrigger(client)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	discoveredResource := newAPIResource()
	trigger.processDiscoveryResource(context.Background(), discoveredResource)

	actions := client.Actions()
	expectCreateStorageVersionMigrationAction(t, actions[3])
	verifyStorageStateUpdate(t, actions[len(actions)-1], trigger.heartbeat, discoveredResource.StorageVersionHash, []string{v1alpha1.Unknown})
}

func TestProcessDiscoveryResourceStorageMigrationFailed(t *testing.T) {
	client := fake.NewSimpleClientset(
		storageMigration(withFailedCondition()),
		storageState(
			withFreshHeartbeat(),
			withCurrentVersion("newhash"),
			withPersistedVersions(v1alpha1.Unknown),
		),
	)
	trigger := NewMigrationTrigger(client)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	discoveredResource := newAPIResource()
	trigger.processDiscoveryResource(context.Background(), discoveredResource)
	actions := client.Actions()
	expectCreateStorageVersionMigrationAction(t, actions[4])
	verifyStorageStateUpdate(t, actions[len(actions)-1], trigger.heartbeat, discoveredResource.StorageVersionHash, []string{v1alpha1.Unknown})
}

func storageState(options ...func(*v1alpha1.StorageState)) *v1alpha1.StorageState {
	ss := &v1alpha1.StorageState{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageStateName(v1alpha1.GroupVersionResource{Resource: "pods"}),
		},
		Spec: v1alpha1.StorageStateSpec{
			Resource: v1alpha1.GroupResource{Resource: "pods"},
		},
	}
	for _, fn := range options {
		fn(ss)
	}
	return ss
}

func withFreshHeartbeat() func(*v1alpha1.StorageState) {
	return func(ss *v1alpha1.StorageState) {
		ss.Status.LastHeartbeatTime = metav1.NewTime(metav1.Now().Add(-1 * discoveryPeriod))
	}
}

func withStaleHeartbeat() func(*v1alpha1.StorageState) {
	return func(ss *v1alpha1.StorageState) {
		ss.Status.LastHeartbeatTime = metav1.NewTime(metav1.Now().Add(-3 * discoveryPeriod))
	}
}

func withCurrentVersion(version string) func(*v1alpha1.StorageState) {
	return func(ss *v1alpha1.StorageState) {
		ss.Status.CurrentStorageVersionHash = version
	}
}

func withPersistedVersions(versions ...string) func(*v1alpha1.StorageState) {
	return func(ss *v1alpha1.StorageState) {
		ss.Status.PersistedStorageVersionHashes = append(ss.Status.PersistedStorageVersionHashes, versions...)
	}
}

func newMigrationList() *v1alpha1.StorageVersionMigrationList {
	var migrations []v1alpha1.StorageVersionMigration
	for i := 0; i < 3; i++ {
		migration := storageMigration(withName(fmt.Sprintf("migration%d", i)))
		migrations = append(migrations, *migration)
	}
	for i := 3; i < 6; i++ {
		migration := storageMigration(withName(fmt.Sprintf("migration%d", i)), withResource(v1alpha1.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}))
		migrations = append(migrations, *migration)
	}
	return &v1alpha1.StorageVersionMigrationList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageVersionMigrationList",
			APIVersion: "migraiton.k8s.io/v1alpha1",
		},
		Items: migrations,
	}
}

func storageMigration(options ...func(*v1alpha1.StorageVersionMigration)) *v1alpha1.StorageVersionMigration {
	m := &v1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pods",
		},
		Spec: v1alpha1.StorageVersionMigrationSpec{
			Resource: v1alpha1.GroupVersionResource{Version: "v1", Resource: "pods"},
		},
	}
	for _, option := range options {
		option(m)
	}
	return m
}

func withName(name string) func(*v1alpha1.StorageVersionMigration) {
	return func(migration *v1alpha1.StorageVersionMigration) {
		migration.Name = name
	}
}

func withResource(resource v1alpha1.GroupVersionResource) func(*v1alpha1.StorageVersionMigration) {
	return func(migration *v1alpha1.StorageVersionMigration) {
		migration.Spec.Resource = resource
	}
}

func withFailedCondition() func(*v1alpha1.StorageVersionMigration) {
	return func(migration *v1alpha1.StorageVersionMigration) {
		migration.Status.Conditions = append(migration.Status.Conditions, v1alpha1.MigrationCondition{
			Type:   v1alpha1.MigrationFailed,
			Status: v1.ConditionTrue,
		})
	}
}

func newAPIResource() metav1.APIResource {
	return metav1.APIResource{
		Group:              "",
		Name:               "pods",
		Version:            "v1",
		StorageVersionHash: "newhash",
	}
}

func verifyCleanupAndLaunch(t *testing.T, actions []core.Action) {
	if len(actions) != 4 {
		t.Fatalf("expected 4 actions")

	}
	for i := 0; i < 3; i++ {
		a := actions[i]
		d, ok := a.(core.DeleteAction)
		if !ok {
			t.Fatalf("expected delete action")
		}
		r := schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storageversionmigrations"}
		if d.GetResource() != r {
			t.Fatalf("unexpected resource %v", d.GetResource())
		}
		if !strings.Contains(d.GetName(), "migration") {
			t.Fatalf("unexpected name %s", d.GetName())
		}
	}
	expectCreateStorageVersionMigrationAction(t, actions[3])
}

func expectCreateStorageVersionMigrationAction(t *testing.T, action core.Action) *v1alpha1.StorageVersionMigration {
	return expectCreateAction(t, action, schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storageversionmigrations"}).(*v1alpha1.StorageVersionMigration)
}

func expectCreateAction(t *testing.T, action core.Action, gvr schema.GroupVersionResource) runtime.Object {
	c, ok := action.(core.CreateAction)
	if !ok {
		t.Fatalf("expected create action")
	}
	if c.GetResource() != gvr {
		t.Fatalf("unexpected resource %v", c.GetResource())
	}
	return c.GetObject()
}

func verifyStorageStateUpdate(t *testing.T, a core.Action, expectedHeartbeat metav1.Time, expectedCurrentHash string, expectedPersistedHashes []string) {
	u, ok := a.(core.UpdateAction)
	if !ok {
		t.Fatalf("expected update action")
	}
	r := schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storagestates"}
	if u.GetResource() != r {
		t.Fatalf("unexpected resource %v", u.GetResource())
	}
	if u.GetSubresource() != "status" {
		t.Fatalf("unexpected subresource %v", u.GetSubresource())
	}
	ss, ok := u.GetObject().(*v1alpha1.StorageState)
	if !ok {
		t.Fatalf("expected storage state, got %v", ss)
	}
	if a, e := ss.Status.LastHeartbeatTime, expectedHeartbeat; a != e {
		t.Fatalf("expected to update heartbeat, got %v", e)
	}
	if a, e := ss.Status.CurrentStorageVersionHash, expectedCurrentHash; a != e {
		t.Fatalf("expected hash %v, got %v", e, a)
	}
	if a, e := ss.Status.PersistedStorageVersionHashes, expectedPersistedHashes; !reflect.DeepEqual(a, e) {
		t.Fatalf("expected hashes %v, got %v", e, a)
	}
}

// FakeClientset wraps the generated fake Clientset and overrides the Discovery interface
type FakeClientset struct {
	*fake.Clientset
}

func (f *FakeClientset) Discovery() discovery.DiscoveryInterface {
	return &FakeDiscovery{FakeDiscovery: f.Clientset.Discovery().(*fakediscovery.FakeDiscovery)}
}

// FakeDiscovery wraps the client-go FakeDiscovery and overrides the ServerPreferredResources method
type FakeDiscovery struct {
	*fakediscovery.FakeDiscovery
}

func (f *FakeDiscovery) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	failedGroups := make(map[schema.GroupVersion]error)
	gv := schema.GroupVersion{Group: "test.k8s.io", Version: "v1"}
	failedGroups[gv] = fmt.Errorf("test partial discovery failure")
	return []*metav1.APIResourceList{
		{
			APIResources: []metav1.APIResource{newAPIResource()},
		},
	}, &discovery.ErrGroupDiscoveryFailed{Groups: failedGroups}

}

func TestProcessDiscoveryPartialFailure(t *testing.T) {
	client := fake.NewSimpleClientset(newMigrationList())
	// overrides the ServerPreferredResources method of the simple clientset
	trigger := NewMigrationTrigger(&FakeClientset{Clientset: client})
	stopCh := make(chan struct{})
	defer close(stopCh)
	go trigger.migrationInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, trigger.migrationInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	trigger.heartbeat = metav1.Now()
	// let trigger controller invokes ServerPreferredResources method to discover
	// the API resources
	trigger.processDiscovery(context.TODO())
	actions := client.Actions()
	verifyCleanupAndLaunch(t, actions[3:7])

	c, ok := actions[8].(core.CreateAction)
	if !ok {
		t.Fatalf("expected create action")
	}
	r := schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storagestates"}
	if c.GetResource() != r {
		t.Fatalf("unexpected resource %v", c.GetResource())
	}

	verifyStorageStateUpdate(t, actions[9], trigger.heartbeat, newAPIResource().StorageVersionHash, []string{v1alpha1.Unknown})
}
