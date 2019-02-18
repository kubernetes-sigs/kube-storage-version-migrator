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
	informer := newStatusIndexedInformer(client)

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
