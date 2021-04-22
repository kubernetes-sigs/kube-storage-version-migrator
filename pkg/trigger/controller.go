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
	"time"

	apiserverinternalv1alpha1 "k8s.io/api/apiserverinternal/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	apiserverinternalinformers "k8s.io/client-go/informers/apiserverinternal/v1alpha1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	migrationv1alpha1 "sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	migrationclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/controller"
)

var (
	backoff = wait.Backoff{
		Steps:    6,
		Duration: 10 * time.Millisecond,
		Factor:   5.0,
		Jitter:   0.1,
	}
)

const (
	// The migration trigger controller redo the discovery every discoveryPeriod.
	discoveryPeriod = 10 * time.Minute
)

type MigrationTrigger struct {
	client                 migrationclient.Interface
	kubeClient             kubernetes.Interface
	migrationInformer      cache.SharedIndexInformer
	storageVersionInformer cache.SharedIndexInformer
	queue                  workqueue.RateLimitingInterface
	storageVersionQueue    workqueue.RateLimitingInterface
	// The timestamp of last time discovery is performed.
	heartbeat metav1.Time

	lastSeenTransitionTime map[string]metav1.Time
}

func NewMigrationTrigger(c migrationclient.Interface, kubeClient kubernetes.Interface) *MigrationTrigger {
	mt := &MigrationTrigger{
		client:     c,
		kubeClient: kubeClient,
		// TODO: share one with the kubemigrator.go.
		migrationInformer: controller.NewStatusAndResourceIndexedInformer(c),
		storageVersionInformer: apiserverinternalinformers.NewStorageVersionInformer(kubeClient,
			0,
			cache.Indexers{}),
		queue:                  workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "migration_triggering_controller"),
		storageVersionQueue:    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "migration_triggering_controller_storage_version"),
		lastSeenTransitionTime: make(map[string]metav1.Time),
	}
	mt.migrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    mt.addResource,
		UpdateFunc: mt.updateResource,
		DeleteFunc: mt.deleteResource,
	})
	mt.storageVersionInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    mt.addStorageVersion,
		UpdateFunc: mt.updateStorageVersion,
	})

	return mt
}

func (mt *MigrationTrigger) dequeue() <-chan interface{} {
	work := make(chan interface{})
	go func() {
		for {
			item, quit := mt.queue.Get()
			if quit {
				close(work)
				return
			}
			work <- item
		}
	}()
	return work
}

func (mt *MigrationTrigger) dequeueStorageVersion() <-chan interface{} {
	work := make(chan interface{})
	go func() {
		for {
			item, quit := mt.storageVersionQueue.Get()
			if quit {
				close(work)
				return
			}
			work <- item
		}
	}()
	return work
}

// queueItem is the object in the workqueue.
type queueItem struct {
	// the namespace of the storageVersionMigration object.
	namespace string
	// the name of the storageVersionMigration object.
	name string
	// the resource the storageVersionMigration object is about.
	resource migrationv1alpha1.GroupVersionResource
}

func (mt *MigrationTrigger) addResource(obj interface{}) {
	m, ok := obj.(*migrationv1alpha1.StorageVersionMigration)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("expected StorageVersionMigration, got %#v", reflect.TypeOf(obj)))
		return
	}
	mt.enqueueResource(m)
}

func (mt *MigrationTrigger) deleteResource(obj interface{}) {
	m, ok := obj.(*migrationv1alpha1.StorageVersionMigration)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %+v", obj))
			return
		}
		m, ok = tombstone.Obj.(*migrationv1alpha1.StorageVersionMigration)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a StorageVersionMigration %#v", obj))
			return
		}
	}
	mt.enqueueResource(m)
}

func (mt *MigrationTrigger) updateResource(oldObj interface{}, obj interface{}) {
	mt.addResource(obj)
}

func (mt *MigrationTrigger) enqueueResource(migration *migrationv1alpha1.StorageVersionMigration) {
	it := &queueItem{
		namespace: migration.Namespace,
		name:      migration.Name,
		resource:  migration.Spec.Resource,
	}
	mt.queue.Add(it)
}

func (mt *MigrationTrigger) addStorageVersion(obj interface{}) {
	sv, ok := obj.(*apiserverinternalv1alpha1.StorageVersion)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("expected StorageVersion, got %#v", reflect.TypeOf(obj)))
		return
	}
	mt.enqueueStorageVersion(sv)
}

func (mt *MigrationTrigger) updateStorageVersion(_ interface{}, obj interface{}) {
	mt.addStorageVersion(obj)
}

func (mt *MigrationTrigger) enqueueStorageVersion(sv *apiserverinternalv1alpha1.StorageVersion) {
	mt.storageVersionQueue.Add(sv.Name)
}

func (mt *MigrationTrigger) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()
	go mt.migrationInformer.Run(ctx.Done())
	go mt.storageVersionInformer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), mt.migrationInformer.HasSynced, mt.storageVersionInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Unable to sync caches"))
		return
	}
	work := mt.dequeue()
	workStorageVersion := mt.dequeueStorageVersion()

	// We need to run the discovery routine and the migration management
	// routine in serial. Otherwise, they can corrupt
	// storageState.status.persistedStorageVersions.
	//
	// The discovery routine does the following for each resource:
	// a. checks if storageState.status.currentStorageVersion == discovered.storageVersion
	// b. if not, cleans existing migrations
	// c. launches a new migration
	// d. updates the storageState.status.currentStorageVersion and .persistedStorageVersions.
	//
	// The migration management routine does the following:
	// 1. gets a migration object from the workqueue
	// 2. gets the latest migration object from the apiserver
	// 3. if the latest migration object shows the migration has completed
	// successfully, updates the storageState.status.persistedStorageVersions
	// to only contain storageState.status.currentStorageVersion.
	//
	// The PersistedStorageVersions will be corrupted if the above steps
	// interleave in this order: 2, b, c, d, 3
	//
	// TODO: if we let the migration note down the currentStorageVersion,
	// we can avoid the race.

	mt.heartbeat = metav1.Now()
	for {
		select {
		case wSV := <-workStorageVersion:
			err := mt.processStorageVersionQueue(ctx, wSV.(string))
			if err == nil {
				mt.storageVersionQueue.Forget(wSV)
				mt.storageVersionQueue.Done(wSV)
				break
			}
			utilruntime.HandleError(fmt.Errorf("failed to process storage version %v: %v", wSV, err))
			mt.storageVersionQueue.AddRateLimited(wSV)
			mt.storageVersionQueue.Done(wSV)
		case w := <-work:
			err := mt.processQueue(ctx, w)
			if err == nil {
				mt.queue.Forget(w)
				mt.queue.Done(w)
				break
			}
			utilruntime.HandleError(fmt.Errorf("failed to process %v: %v", w, err))
			mt.queue.AddRateLimited(w)
			mt.queue.Done(w)
		case <-ctx.Done():
			return
		}
	}
}
