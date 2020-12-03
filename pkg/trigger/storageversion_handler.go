/*
Copyright 2020 The Kubernetes Authors.

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
	"strings"

	apiserverinternalv1alpha1 "k8s.io/api/apiserverinternal/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

func (mt *MigrationTrigger) processStorageVersion(ctx context.Context, sv *apiserverinternalv1alpha1.StorageVersion) error {
	klog.V(2).Infof("processing storage version %#v", sv)
	idx := strings.LastIndex(sv.Name, ".")

	group := ""
	version := *sv.Status.CommonEncodingVersion
	seg := strings.Split(*sv.Status.CommonEncodingVersion, "/")
	if len(seg) == 2 {
		group = seg[0]
		version = seg[1]
	}

	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: sv.Name[idx+1:],
	}
	ssName := gvr.GroupResource().String()
	ss, getErr := mt.client.MigrationV1alpha1().StorageStates().Get(ctx, ssName, metav1.GetOptions{})
	if getErr != nil && !errors.IsNotFound(getErr) {
		return getErr
	}
	found := getErr == nil
	stale := found && mt.staleStorageState(ss)
	foundCondition := false
	var lastTransitionTime metav1.Time
	for _, condition := range sv.Status.Conditions {
		if condition.Type == apiserverinternalv1alpha1.AllEncodingVersionsEqual && condition.Status == apiserverinternalv1alpha1.ConditionTrue {
			foundCondition = true
			lastTransitionTime = condition.LastTransitionTime
		}
	}
	storageVersionChanged := found && (ss.Status.CurrentStorageVersionHash != *sv.Status.CommonEncodingVersion ||
		lastTransitionTime != mt.lastSeenTransitionTime[sv.Name]) && foundCondition
	if foundCondition {
		mt.lastSeenTransitionTime[sv.Name] = lastTransitionTime
	}
	needsMigration := found && !mt.isMigrated(ss) && !mt.hasPendingOrRunningMigration(gvr.GroupResource())
	relaunchMigration := stale || !found || storageVersionChanged || needsMigration

	if stale {
		if err := mt.client.MigrationV1alpha1().StorageStates().Delete(ctx, ssName, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	if relaunchMigration {
		// Note that this means historical migration objects are deleted.
		if err := mt.relaunchMigration(ctx, gvr); err != nil {
			utilruntime.HandleError(err)
		}
	}

	// always update status.heartbeat, sometimes update the version hashes.
	mt.updateStorageState(ctx, *sv.Status.CommonEncodingVersion, gvr.GroupResource())
	return nil
}

func (mt *MigrationTrigger) processStorageVersionQueue(ctx context.Context, key string) error {
	mt.heartbeat = metav1.Now()
	sv, err := mt.kubeClient.InternalV1alpha1().StorageVersions().Get(ctx, key, metav1.GetOptions{})
	if err == nil && sv.Status.CommonEncodingVersion != nil {
		return mt.processStorageVersion(ctx, sv)
	}
	if err != nil && errors.IsNotFound(err) {
		// The resource is no longer served by any API server. The generic
		// garbage collector is supposed to clean up the objects.
		return nil
	}
	return err
}
