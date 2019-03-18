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
	"fmt"
	"time"

	migrationv1alpha1 "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clients/clientset/typed/migration/v1alpha1"
	"k8s.io/api/core/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
)

type initializer struct {
	discovery       *migrationDiscovery
	crdClient       v1beta1.CustomResourceDefinitionInterface
	namespaceClient corev1.NamespaceInterface
	migrationClient v1alpha1.StorageVersionMigrationInterface
}

func NewInitializer(
	disocveryClient discovery.ServerResourcesInterface,
	crdClient v1beta1.CustomResourceDefinitionInterface,
	apiserviceClient apiregistrationv1.APIServiceInterface,
	namespaceClient corev1.NamespaceInterface,
	migrationGetter v1alpha1.StorageVersionMigrationsGetter,
) *initializer {
	d := NewDiscovery(disocveryClient, crdClient, apiserviceClient)
	return &initializer{
		discovery:       d,
		crdClient:       crdClient,
		namespaceClient: namespaceClient,
		migrationClient: migrationGetter.StorageVersionMigrations(namespaceName),
	}
}

const (
	// TODO: get the namespace name from the enviroment variable
	namespaceName   = "kube-storage-migration"
	singularCRDName = "storageversionmigration"
	pluralCRDName   = "storageversionmigrations"
	kind            = "StorageVersionMigration"
	listKind        = "StorageVersionMigrationList"
)

func migrationCRD() *apiextensionsv1beta1.CustomResourceDefinition {
	return &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s.%s", pluralCRDName, migrationv1alpha1.GroupName),
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   migrationv1alpha1.GroupName,
			Version: migrationv1alpha1.SchemeGroupVersion.Version,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:   pluralCRDName,
				Singular: singularCRDName,
				Kind:     kind,
				ListKind: listKind,
			},
			Scope: apiextensionsv1beta1.NamespaceScoped,
			Subresources: &apiextensionsv1beta1.CustomResourceSubresources{
				Status: &apiextensionsv1beta1.CustomResourceSubresourceStatus{},
			},
		},
	}
}

func migrationForResource(resource schema.GroupVersionResource) *migrationv1alpha1.StorageVersionMigration {
	var name string
	if len(resource.Group) != 0 {
		name = fmt.Sprintf("%s.%s.%s-", resource.Group, resource.Version, resource.Resource)
	} else {
		name = fmt.Sprintf("%s.%s-", resource.Version, resource.Resource)
	}
	return &migrationv1alpha1.StorageVersionMigration{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name,
		},
		Spec: migrationv1alpha1.StorageVersionMigrationSpec{
			Resource: migrationv1alpha1.GroupVersionResource{
				Group:    resource.Group,
				Version:  resource.Version,
				Resource: resource.Resource,
			},
		},
	}
}

func (init *initializer) initializeCRD() error {
	crdName := fmt.Sprintf("%s.%s", pluralCRDName, migrationv1alpha1.GroupName)
	// check if crd already exists
	_, err := init.crdClient.Get(crdName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if err != nil && errors.IsNotFound(err) {
		_, err := init.crdClient.Create(migrationCRD())
		return err
	}

	// delete the crd, and wait for it's deletion
	if err := init.crdClient.Delete(crdName, nil); err != nil {
		return err
	}
	err = wait.PollImmediate(500*time.Millisecond, 30*time.Second, func() (bool, error) {
		_, err := init.crdClient.Get(crdName, metav1.GetOptions{})
		if err == nil {
			return false, nil
		}
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err != nil {
		return err
	}
	_, err = init.crdClient.Create(migrationCRD())
	return err
}

// TODO: remove this function. Users will use provided yaml files to create the
// namespace, and then create the initializer in the namespace.
func (init *initializer) initializeNamespace() error {
	_, err := init.namespaceClient.Get(namespaceName, metav1.GetOptions{})
	if (err != nil && !errors.IsNotFound(err)) || err == nil {
		return err
	}
	_, err = init.namespaceClient.Create(&v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
		},
	})
	return err
}

func (init *initializer) Initialize() error {
	// TODO: remove deployment code.
	if err := init.initializeCRD(); err != nil {
		return err
	}
	// TODO: remove deployment code.
	if err := init.initializeNamespace(); err != nil {
		return err
	}

	// run discovery
	resources, err := init.discovery.FindMigratableResources()
	if err != nil {
		return err
	}

	for _, r := range resources {
		if _, err := init.migrationClient.Create(migrationForResource(r)); err != nil {
			return err
		}
	}
	return nil
}
