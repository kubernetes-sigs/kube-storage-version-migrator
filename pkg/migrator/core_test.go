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

package migrator

import (
	"encoding/json"
	"fmt"
	"testing"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clitesting "k8s.io/client-go/testing"
)

func newPod(name, namespace string) v1.Pod {
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func newPodList(num int) v1.PodList {
	var pods []v1.Pod
	for i := 0; i < num; i++ {
		pod := newPod(fmt.Sprintf("pod%d", i), fmt.Sprintf("namespace%d", i))
		pods = append(pods, pod)
	}
	return v1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PodList",
			APIVersion: "v1",
		},
		Items: pods,
	}
}

func newNode(name string) v1.Node {
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func newNodeList(num int) v1.NodeList {
	var nodes []v1.Node
	for i := 0; i < num; i++ {
		node := newNode(fmt.Sprintf("node%d", i))
		nodes = append(nodes, node)
	}
	return v1.NodeList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeList",
			APIVersion: "v1",
		},
		Items: nodes,
	}
}

func toUnstructuredListOrDie(l interface{}) *unstructured.UnstructuredList {
	data, err := json.Marshal(l)
	if err != nil {
		panic(err)
	}
	uncastObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, data)
	if err != nil {
		panic(err)
	}
	ret, ok := uncastObj.(*unstructured.UnstructuredList)
	if !ok {
		panic(fmt.Sprintf("expected *unstructured.UnstructuredList, got %#v", uncastObj))
	}
	return ret
}

func TestMigrateList(t *testing.T) {
	podList := newPodList(100)
	client := fake.NewSimpleDynamicClient(scheme.Scheme, &podList)

	// injects errors.
	pod51FirstTry := true
	pod51Retried := false
	client.Fake.PrependReactor("update", "pods", func(a clitesting.Action) (bool, runtime.Object, error) {
		ua, ok := a.(clitesting.UpdateAction)
		if !ok {
			t.Fatalf("expected UpdateAction")
		}
		name, err := metadataAccessor.Name(ua.GetObject())
		if err != nil {
			t.Fatal(err)
		}
		// inject unrecoverable error for pod 50.
		if name == "pod50" {
			return true, nil, errors.NewMethodNotSupported(v1.Resource("pods"), "update")
		}
		// inject retriable error for pod 51.
		if name == "pod51" {
			if pod51FirstTry {
				pod51FirstTry = false
				return true, nil, errors.NewTimeoutError("retriable error", 1)
			} else {
				pod51Retried = true
				return true, nil, nil
			}
		}

		// TODO: enable this injection when
		// https://github.com/kubernetes/kubernetes/pull/69125 is
		// merged. Otherwise the fake dynamic client panics when
		// handling get request.

		// // inject update conflict error for pod 52.
		// if name == "pod52" {
		// 	if pod52FirstTry {
		// 		pod52FirstTry = false
		// 		return true, nil, errors.NewConflict(v1.Resource("pods"), "pod52", nil)
		// 	} else {
		// 		pod52Retried = true
		// 		return true, nil, nil
		// 	}
		// }

		// TODO: add a test that has an update conflict, and then the
		// first try of GET failed. This is blocked by #69125 as well.

		// Not found error should be ignored
		if name == "pod53" {
			return true, nil, errors.NewNotFound(v1.Resource("pods"), "pod53")
		}
		return false, nil, nil
	})

	migrator := NewMigrator(v1.SchemeGroupVersion.WithResource("pods"), client)
	migratorError := migrator.migrateList(toUnstructuredListOrDie(podList))

	// Validating sent requests.
	nsSet := sets.NewString()
	podSet := sets.NewString()
	actions := client.Actions()
	for _, a := range actions {
		namespace, verb := a.GetNamespace(), a.GetVerb()
		var name string
		if verb != "update" {
			t.Errorf("unexpected %q request %v", verb, a)
		}
		ua, ok := a.(clitesting.UpdateAction)
		if !ok {
			t.Fatalf("expected UpdateAction")
		}
		obj := ua.GetObject()
		var err error
		name, err = metadataAccessor.Name(obj)
		if err != nil {
			t.Fatal(err)
		}
		nsSet.Insert(namespace)
		podSet.Insert(name)
	}
	for i := 0; i < 100; i++ {
		if !nsSet.Has(fmt.Sprintf("namespace%d", i)) {
			t.Errorf("missing namespace %d", i)
		}
		if !podSet.Has(fmt.Sprintf("pod%d", i)) {
			t.Errorf("missing pod %d", i)
		}
	}

	if !pod51Retried {
		t.Errorf("expected migrator to retry pod 51")
	}

	// Verify that only the non-retriable error for pod50 is recorded.
	if migratorError.Error() != `update is not supported on resources of kind "pods"` {
		t.Errorf("unexpected error message %s", migratorError)
	}
}

func TestMigrateListClusterScoped(t *testing.T) {
	nodeList := newNodeList(100)
	client := fake.NewSimpleDynamicClient(scheme.Scheme, &nodeList)

	migrator := NewMigrator(v1.SchemeGroupVersion.WithResource("nodes"), client)
	err := migrator.migrateList(toUnstructuredListOrDie(nodeList))
	if err != nil {
		t.Errorf("unexpected migration error, %v", err)
	}

	// Validating sent requests.
	nodeSet := sets.NewString()
	actions := client.Actions()
	for _, a := range actions {
		namespace, verb := a.GetNamespace(), a.GetVerb()
		if namespace != "" {
			t.Errorf("unexpected non-empty namespace %s", namespace)
		}
		if verb != "update" {
			t.Errorf("unexpected %q request %v", verb, a)
		}
		ua, ok := a.(clitesting.UpdateAction)
		if !ok {
			t.Fatalf("expected UpdateAction")
		}
		obj := ua.GetObject()
		name, err := metadataAccessor.Name(obj)
		if err != nil {
			t.Fatal(err)
		}
		nodeSet.Insert(name)
	}
	for i := 0; i < 100; i++ {
		if !nodeSet.Has(fmt.Sprintf("node%d", i)) {
			t.Errorf("missing node %d", i)
		}
	}
}
