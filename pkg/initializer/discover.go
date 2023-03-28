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

package initializer

import (
	"context"
	"strings"

	v1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/klog/v2"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
)

type migrationDiscovery struct {
	discoveryClient  discovery.ServerResourcesInterface
	crdClient        v1.CustomResourceDefinitionInterface
	apiserviceClient apiregistrationv1.APIServiceInterface
}

// NewDiscovery returns a migrationDiscovery struct.
func NewDiscovery(
	discoveryClient discovery.ServerResourcesInterface,
	crdClient v1.CustomResourceDefinitionInterface,
	apiserviceClient apiregistrationv1.APIServiceInterface,
) *migrationDiscovery {
	return &migrationDiscovery{
		discoveryClient:  discoveryClient,
		crdClient:        crdClient,
		apiserviceClient: apiserviceClient,
	}
}

var blackListResources = sets.NewString(
	"events",
)

// FindMigratableResources finds all the resources that potentially need
// migration. Although all migratable resources are accessible via multiple
// versions, the returned list only include one version.
//
// It builds the list in these steps:
// 1. build a map from resource name to the groupVersions, excluding subresources, custom resources, or aggregated resources.
// 2. exclude all the resource that is only available from one groupVersions.
// 3. exclude the resource that does not support "list" and "update" (thus not migratable).
//
// Note that the above is based on intuition. There are two potential problems:
// a. It's possible that a set of objects is accessible from different groups and different resource names,
// b. It's possible that two groups support the same resource name but refer to different sets of objects.
// though Kubernetes built-in resources don't have such cases yet.
//
// TODO: if https://github.com/kubernetes/community/pull/2805 is realized,
// refactor this method to build resource list accurately.
func (d *migrationDiscovery) FindMigratableResources(ctx context.Context) ([]schema.GroupVersionResource, error) {
	customGroups, err := d.findCustomGroups(ctx)
	if err != nil {
		return nil, err
	}
	aggregatedGroups, err := d.findAggregatedGroups(ctx)
	if err != nil {
		return nil, err
	}
	resourceToGroupVersions := make(map[string][]schema.GroupVersion)
	_, resourceLists, err := d.discoveryClient.ServerGroupsAndResources()
	if err != nil {
		return nil, err
	}
	for _, resourceList := range resourceLists {
		gv, err := schema.ParseGroupVersion(resourceList.GroupVersion)
		if err != nil {
			klog.Errorf("cannot parse group version %s, ignored", resourceList.GroupVersion)
			continue
		}
		if customGroups.Has(gv.Group) {
			klog.V(4).Infof("ignored group %v because it's a custom group", gv.Group)
			continue
		}
		if aggregatedGroups.Has(gv.Group) {
			klog.V(4).Infof("ignored group %v because it's an aggregated group", gv.Group)
			continue
		}
		for _, r := range resourceList.APIResources {
			// ignore subresources
			if strings.Contains(r.Name, "/") {
				continue
			}
			if blackListResources.Has(r.Name) {
				continue
			}
			// ignore resources that cannot be listed and updated
			if !sets.NewString(r.Verbs...).HasAll("list", "update") {
				continue
			}
			gvs := resourceToGroupVersions[r.Name]
			gvs = append(gvs, gv)
			resourceToGroupVersions[r.Name] = gvs
		}
	}

	var ret []schema.GroupVersionResource
	for resource, groupVersions := range resourceToGroupVersions {
		if len(groupVersions) == 1 {
			continue
		}
		ret = append(ret, groupVersions[0].WithResource(resource))
	}
	return ret, nil
}

func (d *migrationDiscovery) findCustomGroups(ctx context.Context) (sets.String, error) {
	ret := sets.NewString()
	l, err := d.crdClient.List(ctx, metav1.ListOptions{})
	if err != nil {
		return ret, err
	}
	for _, crd := range l.Items {
		ret.Insert(crd.Spec.Group)
	}
	return ret, nil
}

func (d *migrationDiscovery) findAggregatedGroups(ctx context.Context) (sets.String, error) {
	ret := sets.NewString()
	l, err := d.apiserviceClient.List(ctx, metav1.ListOptions{})
	if err != nil {
		return ret, err
	}
	for _, apiservice := range l.Items {
		if apiservice.Spec.Service != nil {
			ret.Insert(apiservice.Spec.Group)
		}
	}
	return ret, nil
}
