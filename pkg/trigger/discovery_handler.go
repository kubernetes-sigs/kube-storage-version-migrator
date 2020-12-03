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

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	migrationv1alpha1 "sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/controller"
)

// cleanMigrations removes all storageVersionMigrations whose .spec.resource == r.
func (mt *MigrationTrigger) cleanMigrations(ctx context.Context, gr schema.GroupResource) error {
	// Using the cache to find all matching migrations.
	// The delay of the cache shouldn't matter in practice, because
	// existing migrations are created by previous discovery cycles, they
	// have at least discoveryPeriod to enter the informer's cache.
	idx := mt.migrationInformer.GetIndexer()
	l, err := idx.ByIndex(controller.ResourceIndex, gr.String())
	if err != nil {
		return err
	}
	for _, m := range l {
		mm, ok := m.(*migrationv1alpha1.StorageVersionMigration)
		if !ok {
			return fmt.Errorf("expected StorageVersionMigration, got %#v", reflect.TypeOf(m))
		}
		err := mt.client.MigrationV1alpha1().StorageVersionMigrations().Delete(ctx, mm.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("unexpected error deleting migration %s, %v", mm.Name, err)
		}
	}
	return nil
}

func (mt *MigrationTrigger) launchMigration(ctx context.Context, gvr schema.GroupVersionResource) error {
	m := &migrationv1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: gvr.GroupResource().String() + "-",
		},
		Spec: migrationv1alpha1.StorageVersionMigrationSpec{
			Resource: migrationv1alpha1.GroupVersionResource{
				Group:    gvr.Group,
				Version:  gvr.Version,
				Resource: gvr.Resource,
			},
		},
	}
	_, err := mt.client.MigrationV1alpha1().StorageVersionMigrations().Create(ctx, m, metav1.CreateOptions{})
	return err
}

// relaunchMigration cleans existing migrations for the resource, and launch a new one.
func (mt *MigrationTrigger) relaunchMigration(ctx context.Context, gvr schema.GroupVersionResource) error {
	if err := mt.cleanMigrations(ctx, gvr.GroupResource()); err != nil {
		return err
	}
	return mt.launchMigration(ctx, gvr)

}

func (mt *MigrationTrigger) newStorageState(gr schema.GroupResource) *migrationv1alpha1.StorageState {
	return &migrationv1alpha1.StorageState{
		ObjectMeta: metav1.ObjectMeta{
			Name: gr.String(),
		},
		Spec: migrationv1alpha1.StorageStateSpec{
			Resource: migrationv1alpha1.GroupResource{
				Group:    gr.Group,
				Resource: gr.Resource,
			},
		},
	}
}

func (mt *MigrationTrigger) updateStorageState(ctx context.Context, currentHash string, gr schema.GroupResource) error {
	// We will retry on any error, because failing to update the
	// heartbeat of the storageState can lead to redo migration, which is
	// costly.
	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		ss, err := mt.client.MigrationV1alpha1().StorageStates().Get(ctx, gr.String(), metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			utilruntime.HandleError(err)
			return false, nil
		}
		if err != nil && errors.IsNotFound(err) {
			// Note that the apiserver resets the status field for
			// the POST request. We need to update via the status
			// endpoint.
			ss, err = mt.client.MigrationV1alpha1().StorageStates().Create(ctx, mt.newStorageState(gr), metav1.CreateOptions{})
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
		_, err = mt.client.MigrationV1alpha1().StorageStates().UpdateStatus(ctx, ss, metav1.UpdateOptions{})
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

func (mt *MigrationTrigger) isMigrated(ss *migrationv1alpha1.StorageState) bool {
	if len(ss.Status.PersistedStorageVersionHashes) != 1 {
		return false
	}
	return ss.Status.CurrentStorageVersionHash == ss.Status.PersistedStorageVersionHashes[0]
}

func (mt *MigrationTrigger) hasPendingOrRunningMigration(gr schema.GroupResource) bool {
	// get the corresponding StorageVersionMigration resource
	migrations, err := mt.migrationInformer.GetIndexer().ByIndex(controller.ResourceIndex, gr.String())
	if err != nil {
		utilruntime.HandleError(err)
		return false
	}
	for _, migration := range migrations {
		m := migration.(*migrationv1alpha1.StorageVersionMigration)
		if controller.HasCondition(m, migrationv1alpha1.MigrationSucceeded) || controller.HasCondition(m, migrationv1alpha1.MigrationFailed) {
			continue
		}
		// migration is running or pending
		return true
	}
	return false
}
