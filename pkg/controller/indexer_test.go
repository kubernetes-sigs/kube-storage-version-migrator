/*
Copyright 2018 The Kubernetes Authors.

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

package controller

import (
	"reflect"
	"testing"

	migrationv1alpha1 "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clientset/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

func newMigration(name string, conditionType migrationv1alpha1.MigrationConditionType) *migrationv1alpha1.StorageVersionMigration {
	newCondition := migrationv1alpha1.MigrationCondition{
		Type:   conditionType,
		Status: corev1.ConditionTrue,
	}
	return &migrationv1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: migrationv1alpha1.StorageVersionMigrationStatus{
			Conditions: []migrationv1alpha1.MigrationCondition{
				newCondition,
			},
		},
	}
}

func newMigrationForResource(name string, r migrationv1alpha1.GroupVersionResource) *migrationv1alpha1.StorageVersionMigration {
	return &migrationv1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: migrationv1alpha1.StorageVersionMigrationSpec{
			Resource: r,
		},
	}
}

func TestStatusIndexedInformer(t *testing.T) {
	running := newMigration("Running", migrationv1alpha1.MigrationRunning)
	succeeded := newMigration("Succeeded", migrationv1alpha1.MigrationSucceeded)
	failed := newMigration("Failed", migrationv1alpha1.MigrationFailed)
	pending := &migrationv1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "Pending",
		},
	}
	client := fake.NewSimpleClientset(running, succeeded, failed, pending)
	informer := NewStatusIndexedInformer(client)

	stopCh := make(chan struct{})
	defer close(stopCh)
	go informer.Run(stopCh)

	cache.WaitForCacheSync(stopCh, informer.HasSynced)
	ret, err := informer.GetIndexer().ByIndex(StatusIndex, StatusRunning)
	if err != nil {
		t.Fatal(err)
	}
	if e, a := running, ret[0]; !reflect.DeepEqual(e, a) {
		t.Errorf("expected %v, got %v", e, a)
	}
	ret, err = informer.GetIndexer().ByIndex(StatusIndex, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if e, a := pending, ret[0]; !reflect.DeepEqual(e, a) {
		t.Errorf("expected %v, got %v", e, a)
	}
	ret, err = informer.GetIndexer().ByIndex(StatusIndex, StatusCompleted)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case reflect.DeepEqual(ret[0], failed) && reflect.DeepEqual(ret[1], succeeded):
	case reflect.DeepEqual(ret[1], failed) && reflect.DeepEqual(ret[0], succeeded):
	default:
		t.Errorf("expected one successful, one failed, got %v", ret)
	}
}

func TestResourceIndexedInformer(t *testing.T) {
	podsv1R := migrationv1alpha1.GroupVersionResource{Group: "core", Version: "v1", Resource: "pods"}
	podsv2R := migrationv1alpha1.GroupVersionResource{Group: "core", Version: "v2", Resource: "pods"}
	nodesv1R := migrationv1alpha1.GroupVersionResource{Group: "core", Version: "v1", Resource: "nodes"}
	jobsv1R := migrationv1alpha1.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	podsv1 := newMigrationForResource("podsv1", podsv1R)
	podsv2 := newMigrationForResource("podsv2", podsv2R)
	nodesv1 := newMigrationForResource("nodesv1", nodesv1R)
	jobsv1 := newMigrationForResource("jobsv1", jobsv1R)

	client := fake.NewSimpleClientset(podsv1, podsv2, nodesv1, jobsv1)
	informer := NewStatusAndResourceIndexedInformer(client)

	stopCh := make(chan struct{})
	defer close(stopCh)
	go informer.Run(stopCh)

	cache.WaitForCacheSync(stopCh, informer.HasSynced)
	ret, err := informer.GetIndexer().ByIndex(ResourceIndex, ToIndex(podsv1R))
	if err != nil {
		t.Fatal(err)
	}
	if len(ret) != 2 {
		t.Fatalf("expected two objects, got %v", ret)
	}
	switch {
	case reflect.DeepEqual(ret[0], podsv1) && reflect.DeepEqual(ret[1], podsv2):
	case reflect.DeepEqual(ret[1], podsv2) && reflect.DeepEqual(ret[0], podsv1):
	default:
		t.Errorf("expected either [podsv1, podsv2] or [podsv2, podsv1], got %v", ret)
	}
	ret, err = informer.GetIndexer().ByIndex(ResourceIndex, ToIndex(nodesv1R))
	if err != nil {
		t.Fatal(err)
	}
	if len(ret) != 1 {
		t.Fatalf("expected only one, got %v", ret)
	}
	if e, a := nodesv1, ret[0]; !reflect.DeepEqual(e, a) {
		t.Errorf("expected %v, got %v", e, a)
	}
	ret, err = informer.GetIndexer().ByIndex(ResourceIndex, ToIndex(jobsv1R))
	if err != nil {
		t.Fatal(err)
	}
	if len(ret) != 1 {
		t.Fatalf("expected only one, got %v", ret)
	}
	if e, a := jobsv1, ret[0]; !reflect.DeepEqual(e, a) {
		t.Errorf("expected %v, got %v", e, a)
	}
}
