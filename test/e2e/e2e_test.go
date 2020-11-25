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

package e2e

import (
	"testing"

	"github.com/onsi/ginkgo"
	"k8s.io/klog/v2"
	_ "sigs.k8s.io/kube-storage-version-migrator/test/e2e/tests"
)

func TestE2E(t *testing.T) {
	RunE2ETests(t)
}

func RunE2ETests(t *testing.T) {
	klog.InitFlags(nil)
	klog.Infof("Starting e2e run")
	ginkgo.RunSpecs(t, "Kubernetes Storage Version Migrator e2e suite")
}
