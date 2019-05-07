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
	migrationclient "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/clients/clientset/typed/migration/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

type progressInterface interface {
	save(continueToken string) error
	load() (continueToken string, err error)
}

type progressTracker struct {
	client migrationclient.StorageVersionMigrationInterface
	name   string
}

// NewProgressTracker returns a progress tracker.
func NewProgressTracker(client migrationclient.StorageVersionMigrationInterface, name string) progressInterface {
	return &progressTracker{
		client: client,
		name:   name,
	}
}

func (p *progressTracker) save(continueToken string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		migration, err := p.client.Get(p.name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		migration.Spec.ContinueToken = continueToken
		_, err = p.client.Update(migration)
		return err
	})
}

func (p *progressTracker) load() (continueToken string, err error) {
	migration, err := p.client.Get(p.name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return migration.Spec.ContinueToken, nil
}
