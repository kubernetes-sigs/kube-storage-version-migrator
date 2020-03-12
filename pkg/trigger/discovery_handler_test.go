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

	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clients/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func staleStorageState() *v1alpha1.StorageState {
	return &v1alpha1.StorageState{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageStateName(v1alpha1.GroupVersionResource{Resource: "pods"}),
		},
		Spec: v1alpha1.StorageStateSpec{
			Resource: v1alpha1.GroupResource{Resource: "pods"},
		},
		Status: v1alpha1.StorageStateStatus{
			LastHeartbeatTime: metav1.Time{metav1.Now().Add(-3 * discoveryPeriod)},
		},
	}
}

func freshStorageState() *v1alpha1.StorageState {
	return &v1alpha1.StorageState{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageStateName(v1alpha1.GroupVersionResource{Resource: "pods"}),
		},
		Spec: v1alpha1.StorageStateSpec{
			Resource: v1alpha1.GroupResource{Resource: "pods"},
		},
		Status: v1alpha1.StorageStateStatus{
			CurrentStorageVersionHash:     "newhash",
			PersistedStorageVersionHashes: []string{"newhash"},
			LastHeartbeatTime:             metav1.Time{metav1.Now().Add(-1 * discoveryPeriod)},
		},
	}
}

func freshStorageStateWithOldHash() *v1alpha1.StorageState {
	return &v1alpha1.StorageState{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageStateName(v1alpha1.GroupVersionResource{Resource: "pods"}),
		},
		Spec: v1alpha1.StorageStateSpec{
			Resource: v1alpha1.GroupResource{Resource: "pods"},
		},
		Status: v1alpha1.StorageStateStatus{
			CurrentStorageVersionHash:     "oldhash",
			PersistedStorageVersionHashes: []string{"oldhash"},
			LastHeartbeatTime:             metav1.Time{metav1.Now().Add(-1 * discoveryPeriod)},
		},
	}
}

func newMigrationList() *v1alpha1.StorageVersionMigrationList {
	var migrations []v1alpha1.StorageVersionMigration
	for i := 0; i < 3; i++ {
		migration := newMigration(fmt.Sprintf("migration%d", i), v1alpha1.GroupVersionResource{Version: "v1", Resource: "pods"})
		migrations = append(migrations, migration)
	}
	for i := 3; i < 6; i++ {
		migration := newMigration(fmt.Sprintf("migration%d", i), v1alpha1.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"})
		migrations = append(migrations, migration)
	}
	return &v1alpha1.StorageVersionMigrationList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageVersionMigrationList",
			APIVersion: "migraiton.k8s.io/v1alpha1",
		},
		Items: migrations,
	}
}

func newMigration(name string, r v1alpha1.GroupVersionResource) v1alpha1.StorageVersionMigration {
	return v1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.StorageVersionMigrationSpec{
			Resource: r,
		},
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
	c, ok := actions[3].(core.CreateAction)
	if !ok {
		t.Fatalf("expected create action")
	}
	r := schema.GroupVersionResource{Group: "migration.k8s.io", Version: "v1alpha1", Resource: "storageversionmigrations"}
	if c.GetResource() != r {
		t.Fatalf("unexpected resource %v", c.GetResource())
	}
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
		t.Fatalf("expected to has hash %v, got %v", e, a)
	}
	if a, e := ss.Status.PersistedStorageVersionHashes, expectedPersistedHashes; !reflect.DeepEqual(a, e) {
		t.Fatalf("expected to has hashes %v, got %v", e, a)
	}
}

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
	client := fake.NewSimpleClientset(newMigrationList(), staleStorageState())
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
	if !strings.Contains(d.GetName(), staleStorageState().Name) {
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
	client := fake.NewSimpleClientset(newMigrationList(), freshStorageStateWithOldHash())
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
	client := fake.NewSimpleClientset(newMigrationList(), freshStorageState())
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
