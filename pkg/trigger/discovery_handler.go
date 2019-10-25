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
	"fmt"
	"reflect"

	migrationv1alpha1 "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/controller"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

func (mt *MigrationTrigger) processDiscovery() {
	var resources []*metav1.APIResourceList
	var err2 error
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		resources, err2 = mt.client.Discovery().ServerPreferredResources()
		if err2 != nil {
			utilruntime.HandleError(err2)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Abort processing discovery document: %v", err))
		return
	}
	mt.heartbeat = metav1.Now()
	for _, l := range resources {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("unexpected group version error: %v", err))
			continue
		}
		for _, r := range l.APIResources {
			if r.Group == "" {
				r.Group = gv.Group
			}
			if r.Version == "" {
				r.Version = gv.Version
			}
			mt.processDiscoveryResource(r)
		}
	}
}

func toGroupResource(r metav1.APIResource) migrationv1alpha1.GroupVersionResource {
	return migrationv1alpha1.GroupVersionResource{
		Group:    r.Group,
		Version:  r.Version,
		Resource: r.Name,
	}
}

// cleanMigrations removes all storageVersionMigrations whose .spec.resource == r.
func (mt *MigrationTrigger) cleanMigrations(r metav1.APIResource) error {
	// Using the cache to find all matching migrations.
	// The delay of the cache shouldn't matter in practice, because
	// existing migrations are created by previous discovery cycles, they
	// have at least discoveryPeriod to enter the informer's cache.
	idx := mt.migrationInformer.GetIndexer()
	l, err := idx.ByIndex(controller.ResourceIndex, controller.ToIndex(toGroupResource(r)))
	if err != nil {
		return err
	}
	for _, m := range l {
		mm, ok := m.(*migrationv1alpha1.StorageVersionMigration)
		if !ok {
			return fmt.Errorf("expected StorageVersionMigration, got %#v", reflect.TypeOf(m))
		}
		err := mt.client.MigrationV1alpha1().StorageVersionMigrations().Delete(mm.Name, nil)
		if err != nil {
			return fmt.Errorf("unexpected error deleting migration %s, %v", mm.Name, err)
		}
	}
	return nil
}

func (mt *MigrationTrigger) launchMigration(resource migrationv1alpha1.GroupVersionResource) error {
	m := &migrationv1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: storageStateName(resource) + "-",
		},
		Spec: migrationv1alpha1.StorageVersionMigrationSpec{
			Resource: resource,
		},
	}
	_, err := mt.client.MigrationV1alpha1().StorageVersionMigrations().Create(m)
	return err
}

// relaunchMigration cleans existing migrations for the resource, and launch a new one.
func (mt *MigrationTrigger) relaunchMigration(r metav1.APIResource) error {
	if err := mt.cleanMigrations(r); err != nil {
		return err
	}
	return mt.launchMigration(toGroupResource(r))

}

func (mt *MigrationTrigger) newStorageState(r metav1.APIResource) *migrationv1alpha1.StorageState {
	return &migrationv1alpha1.StorageState{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageStateName(toGroupResource(r)),
		},
		Spec: migrationv1alpha1.StorageStateSpec{
			Resource: migrationv1alpha1.GroupResource{
				Group:    r.Group,
				Resource: r.Name,
			},
		},
	}
}

func (mt *MigrationTrigger) updateStorageState(currentHash string, r metav1.APIResource) error {
	// We will retry on any error, because failing to update the
	// heartbeat of the storageState can lead to redo migration, which is
	// costly.
	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		ss, err := mt.client.MigrationV1alpha1().StorageStates().Get(storageStateName(toGroupResource(r)), metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			utilruntime.HandleError(err)
			return false, nil
		}
		if err != nil && errors.IsNotFound(err) {
			// Note that the apiserver resets the status field for
			// the POST request. We need to update via the status
			// endpoint.
			ss, err = mt.client.MigrationV1alpha1().StorageStates().Create(mt.newStorageState(r))
			if err != nil {
				utilruntime.HandleError(err)
				return false, nil
			}
		}
		if ss.Status.CurrentStorageVersionHash != currentHash {
			ss.Status.CurrentStorageVersionHash = currentHash
			if len(ss.Status.PersistedStorageVersionHashes) == 0 {
				ss.Status.PersistedStorageVersionHashes = []string{migrationv1alpha1.Unknown}
			} else {
				ss.Status.PersistedStorageVersionHashes = append(ss.Status.PersistedStorageVersionHashes, currentHash)
			}
		}
		ss.Status.LastHeartbeatTime = mt.heartbeat
		_, err = mt.client.MigrationV1alpha1().StorageStates().UpdateStatus(ss)
		if err != nil {
			utilruntime.HandleError(err)
			return false, nil
		}
		return true, nil
	})
}

func (mt *MigrationTrigger) staleStorageState(ss *migrationv1alpha1.StorageState) bool {
	return ss.Status.LastHeartbeatTime.Add(2 * discoveryPeriod).Before(mt.heartbeat.Time)
}

func (mt *MigrationTrigger) processDiscoveryResource(r metav1.APIResource) {
	klog.V(4).Infof("processing %#v", r)
	if r.StorageVersionHash == "" {
		klog.V(2).Infof("ignored resource %s because its storageVersionHash is empty", r.Name)
		return
	}
	ss, getErr := mt.client.MigrationV1alpha1().StorageStates().Get(storageStateName(toGroupResource(r)), metav1.GetOptions{})
	if getErr != nil && !errors.IsNotFound(getErr) {
		utilruntime.HandleError(getErr)
		return
	}

	stale := (getErr == nil && mt.staleStorageState(ss))
	storageVersionChanged := (getErr == nil && ss.Status.CurrentStorageVersionHash != r.StorageVersionHash)
	notFound := (getErr != nil && errors.IsNotFound(getErr))

	if stale {
		if err := mt.client.MigrationV1alpha1().StorageStates().Delete(storageStateName(toGroupResource(r)), nil); err != nil {
			utilruntime.HandleError(err)
			return
		}
	}

	if stale || storageVersionChanged || notFound {
		// Note that this means historical migration objects are deleted.
		if err := mt.relaunchMigration(r); err != nil {
			utilruntime.HandleError(err)
		}
	}

	// always update status.heartbeat, sometimes update the version hashes.
	mt.updateStorageState(r.StorageVersionHash, r)
}
