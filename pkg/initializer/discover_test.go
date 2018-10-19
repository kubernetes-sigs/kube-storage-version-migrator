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
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"
)

const (
	customResourceList = `{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "cloud.google.com/v1beta1",
  "resources": [
    {
      "name": "backendconfigs",
      "singularName": "backendconfig",
      "namespaced": true,
      "kind": "BackendConfig",
      "verbs": [
        "delete",
        "deletecollection",
        "get",
        "list",
        "patch",
        "create",
        "update",
        "watch"
      ]
    }
  ]
}`

	aggregatedResourceList = `{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "metrics.k8s.io/v1beta1",
  "resources": [
    {
      "name": "nodes",
      "singularName": "",
      "namespaced": false,
      "kind": "NodeMetrics",
      "verbs": [
        "get",
        "list",
        "update"
      ]
    },
    {
      "name": "pods",
      "singularName": "",
      "namespaced": true,
      "kind": "PodMetrics",
      "verbs": [
        "get",
        "list"
      ]
    }
  ]
}`

	// pods is not migratable, because it's only available in one version
	v1ResourceList = `{
	  "kind": "APIResourceList",
  "groupVersion": "v1",
  "resources": [
    {
      "name": "pods",
      "singularName": "",
      "namespaced": true,
      "kind": "Pod",
      "verbs": [
        "create",
        "delete",
        "deletecollection",
        "get",
        "list",
        "patch",
        "update",
        "watch"
      ],
      "shortNames": [
        "po"
      ],
      "categories": [
        "all"
      ]
    },
    {
      "name": "events",
      "singularName": "",
      "namespaced": true,
      "kind": "Event",
      "verbs": [
        "create",
        "delete",
        "deletecollection",
        "get",
        "list",
        "patch",
        "update",
        "watch"
      ],
      "shortNames": [
        "ev"
      ]
    }
  ]
}`

	// daemonset/status is not migratable, because it's a subresource.
	// replicationcontrollers is not migratable, because it doesn't support "list" or "update".
	extensionsv1beta1ResourceList = `{
  "kind": "APIResourceList",
  "groupVersion": "extensions/v1beta1",
  "resources": [
    {
      "name": "daemonsets",
      "singularName": "",
      "namespaced": true,
      "kind": "DaemonSet",
      "verbs": [
        "create",
        "delete",
        "deletecollection",
        "get",
        "list",
        "patch",
        "update",
        "watch"
      ],
      "shortNames": [
        "ds"
      ]
    },
    {
      "name": "daemonsets/status",
      "singularName": "",
      "namespaced": true,
      "kind": "DaemonSet",
      "verbs": [
        "get",
        "patch",
        "update"
      ]
    },
    {
      "name": "replicationcontrollers",
      "singularName": "",
      "namespaced": true,
      "kind": "ReplicationControllerDummy",
      "verbs": []
    }
  ]
}`
	appsv1ResourceList = `{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "apps/v1",
  "resources": [
    {
      "name": "daemonsets",
      "singularName": "",
      "namespaced": true,
      "kind": "DaemonSet",
      "verbs": [
        "create",
        "delete",
        "deletecollection",
        "get",
        "list",
        "patch",
        "update",
        "watch"
      ],
      "shortNames": [
        "ds"
      ],
      "categories": [
        "all"
      ]
    },
    {
      "name": "daemonsets/status",
      "singularName": "",
      "namespaced": true,
      "kind": "DaemonSet",
      "verbs": [
        "get",
        "patch",
        "update"
      ]
    }
  ]
}`

	// Events are blacklisted from migration.
	eventsv1beta1ResourceList = `{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "events.k8s.io/v1beta1",
  "resources": [
    {
      "name": "events",
      "singularName": "",
      "namespaced": true,
      "kind": "Event",
      "verbs": [
        "create",
        "delete",
        "deletecollection",
        "get",
        "list",
        "patch",
        "update",
        "watch"
      ],
      "shortNames": [
        "ev"
      ]
    }
  ]
}`

	crd = `{
  "kind": "CustomResourceDefinition",
  "apiVersion": "apiextensions.k8s.io/v1beta1",
  "metadata": {
    "name": "backendconfigs.cloud.google.com"
  },
  "spec": {
    "group": "cloud.google.com",
    "version": "v1beta1",
    "names": {
      "plural": "backendconfigs",
      "singular": "backendconfig",
      "kind": "BackendConfig",
      "listKind": "BackendConfigList"
    },
    "scope": "Namespaced"
  },
  "status": {}
}`

	apiserviceAggregated = `{
  "kind": "APIService",
  "apiVersion": "apiregistration.k8s.io/v1",
  "metadata": {
    "name": "v1beta1.metrics.k8s.io"
  },
  "spec": {
    "service": {
      "namespace": "kube-system",
      "name": "metrics-server"
    },
    "group": "metrics.k8s.io",
    "version": "v1beta1",
    "insecureSkipTLSVerify": true,
    "caBundle": null,
    "groupPriorityMinimum": 100,
    "versionPriority": 100
  },
  "status": {}
}`

	apiserviceExtensionsV1beta1 = `{
  "kind": "APIService",
  "apiVersion": "apiregistration.k8s.io/v1",
  "metadata": {
    "name": "v1beta1.extensions"
  },
  "spec": {
    "service": null,
    "group": "extensions",
    "version": "v1beta1",
    "caBundle": null,
    "groupPriorityMinimum": 17900,
    "versionPriority": 1
  },
  "status": {}
}`
)

func fakeAPIResourceLists(t *testing.T) []*metav1.APIResourceList {
	var ret []*metav1.APIResourceList
	for _, data := range []string{customResourceList, aggregatedResourceList, extensionsv1beta1ResourceList, appsv1ResourceList, v1ResourceList, eventsv1beta1ResourceList} {
		l := &metav1.APIResourceList{}
		err := json.Unmarshal([]byte(data), l)
		if err != nil {
			t.Fatal(err)
		}
		ret = append(ret, l)
	}
	return ret
}

func fakeCRDs(t *testing.T) []runtime.Object {
	c := &v1beta1.CustomResourceDefinition{}
	err := json.Unmarshal([]byte(crd), c)
	if err != nil {
		t.Fatal(err)
	}
	return []runtime.Object{c}
}

func fakeAPIServices(t *testing.T) []runtime.Object {
	var ret []runtime.Object
	for _, data := range []string{apiserviceExtensionsV1beta1, apiserviceAggregated} {
		a := &v1.APIService{}
		err := json.Unmarshal([]byte(data), a)
		if err != nil {
			t.Fatal(err)
		}
		ret = append(ret, a)
	}
	return ret
}

func TestFindMigratableResources(t *testing.T) {
	kubernetes := fake.NewSimpleClientset()
	kubernetes.Fake.Resources = fakeAPIResourceLists(t)

	crdClient := apiextensionsfake.NewSimpleClientset(fakeCRDs(t)...).ApiextensionsV1beta1().CustomResourceDefinitions()
	apiserviceClient := aggregatorfake.NewSimpleClientset(fakeAPIServices(t)...).ApiregistrationV1().APIServices()
	d := NewDiscovery(kubernetes.Discovery(), crdClient, apiserviceClient)
	got, err := d.FindMigratableResources()
	if err != nil {
		t.Fatal(err)
	}
	expected := []schema.GroupVersionResource{schema.GroupVersionResource{Group: "extensions", Version: "v1beta1", Resource: "daemonsets"}}
	if !reflect.DeepEqual(expected, got) {
		t.Errorf("expected %v, got %v", expected, got)
	}
}
