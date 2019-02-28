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
	"fmt"
	"reflect"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
)

var metadataAccessor = meta.NewAccessor()

const (
	defaultChunkLimit  = 500
	defaultConcurrency = 1
)

type migrator struct {
	resource    schema.GroupVersionResource
	client      dynamic.Interface
	progress    *progressTracker
	concurrency int
}

// NewMigrator creates a migrator that can migrate a single resource type.
func NewMigrator(resource schema.GroupVersionResource, client dynamic.Interface, progress *progressTracker) *migrator {
	return &migrator{
		resource:    resource,
		client:      client,
		progress:    progress,
		concurrency: defaultConcurrency,
	}
}

func (m *migrator) get(namespace, name string) (*unstructured.Unstructured, error) {
	// if namespace is empty, .Namespace(namespace) is ineffective.
	return m.client.
		Resource(m.resource).
		Namespace(namespace).
		Get(name, metav1.GetOptions{})
}

func (m *migrator) put(namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	// if namespace is empty, .Namespace(namespace) is ineffective.
	return m.client.
		Resource(m.resource).
		Namespace(namespace).
		Update(obj, metav1.UpdateOptions{})
}

func (m *migrator) list(options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return m.client.
		Resource(m.resource).
		Namespace(metav1.NamespaceAll).
		List(options)
}

// Run migrates all the instances of the resource type managed by the migrator.
func (m *migrator) Run() error {
	continueToken, err := m.progress.load()
	if err != nil {
		return err
	}
	for {
		list, listError := m.list(
			metav1.ListOptions{
				Limit:    defaultChunkLimit,
				Continue: continueToken,
			},
		)
		if listError != nil && !errors.IsResourceExpired(listError) {
			if canRetry(listError) {
				continue
			}
			return listError
		}
		if listError != nil && errors.IsResourceExpired(listError) {
			token, err := inconsistentContinueToken(listError)
			if err != nil {
				return err
			}
			continueToken = token
			err = m.progress.save(continueToken)
			if err != nil {
				utilruntime.HandleError(err)
			}
			continue
		}
		if err := m.migrateList(list); err != nil {
			return err
		}
		token, err := metadataAccessor.Continue(list)
		if err != nil {
			return err
		}
		if len(token) == 0 {
			return nil
		}
		continueToken = token
		err = m.progress.save(continueToken)
		if err != nil {
			utilruntime.HandleError(err)
		}
	}
}

func (m *migrator) migrateList(l *unstructured.UnstructuredList) error {
	stop := make(chan struct{})
	defer close(stop)
	workc := make(chan *unstructured.Unstructured)
	go func() {
		defer close(workc)
		for i := range l.Items {
			select {
			case workc <- &l.Items[i]:
			case <-stop:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(m.concurrency)
	errc := make(chan error)
	for i := 0; i < m.concurrency; i++ {
		go func() {
			defer wg.Done()
			m.worker(stop, workc, errc)
		}()
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	var errors []error
	for err := range errc {
		errors = append(errors, err)
	}
	return utilerrors.NewAggregate(errors)
}

func (m *migrator) worker(stop <-chan struct{}, workc <-chan *unstructured.Unstructured, errc chan<- error) {
	for item := range workc {
		err := m.migrateOneItem(item)
		if err != nil {
			select {
			case errc <- err:
				continue
			case <-stop:
				return
			}
		}
	}
}

func (m *migrator) migrateOneItem(item *unstructured.Unstructured) error {
	namespace, err := metadataAccessor.Namespace(item)
	if err != nil {
		return err
	}
	name, err := metadataAccessor.Name(item)
	if err != nil {
		return err
	}
	getBeforePut := false
	for {
		getBeforePut, err = m.try(namespace, name, item, getBeforePut)
		if err == nil || errors.IsNotFound(err) {
			return nil
		}
		if canRetry(err) {
			if seconds, delay := errors.SuggestsClientDelay(err); delay {
				time.Sleep(time.Duration(seconds) * time.Second)
			}
			continue
		}
		// error is not retriable
		return err
	}
}

// try tries to migrate the single object by PUT. It refreshes the object via
// GET if "get" is true. If the PUT fails due to conflicts, or the GET fails,
// the function requests the next try to GET the new object.
func (m *migrator) try(namespace, name string, item *unstructured.Unstructured, get bool) (bool, error) {
	var err error
	if get {
		item, err = m.get(namespace, name)
		if err != nil {
			return true, err
		}
	}
	_, err = m.put(namespace, name, item)
	if err == nil {
		return false, nil
	}
	return errors.IsConflict(err), err

	// TODO: The oc admin uses a defer function to do bandwidth limiting
	// after doing all operations. The rate limiter is marked as an alpha
	// feature.  Is it better than the built-in qps limit in the REST
	// client? Maybe it's necessary because not all resource types are of
	// the same size?
}

// TODO: move this helper to "k8s.io/apimachinery/pkg/api/errors"
func inconsistentContinueToken(err error) (string, error) {
	status, ok := err.(errors.APIStatus)
	if !ok {
		return "", fmt.Errorf("expected error to implement the APIStatus interface, got %v", reflect.TypeOf(err))
	}
	token := status.Status().ListMeta.Continue
	if len(token) == 0 {
		return "", fmt.Errorf("expected non empty continue token")
	}
	return token, nil
}
