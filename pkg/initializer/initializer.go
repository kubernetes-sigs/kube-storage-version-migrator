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
	"fmt"
	"time"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	migrationv1alpha1 "sigs.k8s.io/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset/typed/migration/v1alpha1"
)

type initializer struct {
	discovery       *migrationDiscovery
	crdClient       apiextensionsv1.CustomResourceDefinitionInterface
	namespaceClient corev1.NamespaceInterface
	migrationClient v1alpha1.StorageVersionMigrationInterface
}

func NewInitializer(
	disocveryClient discovery.ServerResourcesInterface,
	crdClient apiextensionsv1.CustomResourceDefinitionInterface,
	apiserviceClient apiregistrationv1.APIServiceInterface,
	namespaceClient corev1.NamespaceInterface,
	migrationGetter v1alpha1.StorageVersionMigrationsGetter,
) *initializer {
	d := NewDiscovery(disocveryClient, crdClient, apiserviceClient)
	return &initializer{
		discovery:       d,
		crdClient:       crdClient,
		namespaceClient: namespaceClient,
		migrationClient: migrationGetter.StorageVersionMigrations(),
	}
}

const (
	singularCRDName = "storageversionmigration"
	pluralCRDName   = "storageversionmigrations"
	kind            = "StorageVersionMigration"
	listKind        = "StorageVersionMigrationList"
)

func migrationCRD() *v1.CustomResourceDefinition {
	return &v1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "storageversionmigrations.migration.k8s.io",
			Annotations: map[string]string{
				"api-approved.kubernetes.io": "https://github.com/kubernetes/community/pull/2524",
			},
		},
		Spec: v1.CustomResourceDefinitionSpec{
			Group: "migration.k8s.io",
			Names: v1.CustomResourceDefinitionNames{
				Plural:   pluralCRDName,
				Singular: singularCRDName,
				Kind:     kind,
				ListKind: listKind,
			},
			Scope: v1.ClusterScoped,
			Versions: []v1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Subresources: &v1.CustomResourceSubresources{
						Status: &v1.CustomResourceSubresourceStatus{},
					},
					Schema: &v1.CustomResourceValidation{
						OpenAPIV3Schema: &v1.JSONSchemaProps{
							Description: "StorageVersionMigration represents a migration of stored data to the latest storage version.",
							Type:        "object",
							Properties: map[string]v1.JSONSchemaProps{
								"apiVersion": {
									Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources",
									Type:        "string",
								},
								"kind": {
									Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
									Type:        "string",
								},
								"metadata": {
									Type: "object",
								},
								"spec": {
									Description: "Specification of the migration.",
									Type:        "object",
									Required: []string{
										"resource",
									},
									Properties: map[string]v1.JSONSchemaProps{
										"continueToken": {
											Description: "The token used in the list options to get the next chunk of objects to migrate. When the .status.conditions indicates the migration is \"Running\", users can use this token to check the progress of the migration.",
											Type:        "string",
										},
										"resource": {
											Description: "The resource that is being migrated. The migrator sends requests to the endpoint serving the resource. Immutable.",
											Type:        "object",
											Properties: map[string]v1.JSONSchemaProps{
												"group": {
													Description: "The name of the group.",
													Type:        "string",
												},
												"resource": {
													Description: "The name of the resource.",
													Type:        "string",
												},
												"version": {
													Description: "The name of the version.",
													Type:        "string",
												},
											},
										},
									},
								},
								"status": {
									Description: "Status of the migration.",
									Type:        "object",
									Properties: map[string]v1.JSONSchemaProps{
										"conditions": {
											Description: "The latest available observations of the migration's current state.",
											Type:        "array",
											Items: &v1.JSONSchemaPropsOrArray{
												Schema: &v1.JSONSchemaProps{
													Description: "Describes the state of a migration at a certain point.",
													Type:        "object",
													Required: []string{
														"status",
														"type",
													},
													Properties: map[string]v1.JSONSchemaProps{
														"lastUpdateTime": {
															Description: "The last time this condition was updated.",
															Type:        "string",
															Format:      "date-time",
														},
														"message": {
															Description: "A human readable message indicating details about the transition.",
															Type:        "string",
														},
														"reason": {
															Description: "The reason for the condition's last transition.",
															Type:        "string",
														},
														"status": {
															Description: "Status of the condition, one of True, False, Unknown.",
															Type:        "string",
														},
														"type": {
															Description: "Type of the condition.",
															Type:        "string",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
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

func (init *initializer) initializeCRD(ctx context.Context) error {
	crdName := fmt.Sprintf("%s.%s", pluralCRDName, migrationv1alpha1.GroupName)
	// check if crd already exists
	_, err := init.crdClient.Get(ctx, crdName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if err != nil && errors.IsNotFound(err) {
		_, err := init.crdClient.Create(ctx, migrationCRD(), metav1.CreateOptions{})
		return err
	}

	// delete the crd, and wait for it's deletion
	if err := init.crdClient.Delete(ctx, crdName, metav1.DeleteOptions{}); err != nil {
		return err
	}
	err = wait.PollImmediate(500*time.Millisecond, 30*time.Second, func() (bool, error) {
		_, err := init.crdClient.Get(ctx, crdName, metav1.GetOptions{})
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
	_, err = init.crdClient.Create(ctx, migrationCRD(), metav1.CreateOptions{})
	return err
}

func (init *initializer) Initialize(ctx context.Context) error {
	// TODO: remove deployment code.
	if err := init.initializeCRD(ctx); err != nil {
		return err
	}

	// run discovery
	resources, err := init.discovery.FindMigratableResources(ctx)
	if err != nil {
		return err
	}

	for _, r := range resources {
		if _, err := init.migrationClient.Create(ctx, migrationForResource(r), metav1.CreateOptions{}); err != nil {
			return err
		}
	}
	return nil
}
