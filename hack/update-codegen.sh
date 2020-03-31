#!/usr/bin/env bash

# Copyright 2014 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

THIS_REPO="sigs.k8s.io/kube-storage-version-migrator"
API_PKG="${THIS_REPO}/pkg/apis/migration/v1alpha1"
# Absolute path to this repo
THIS_REPO_ABSOLUTE="$(cd "$(dirname "${BASH_SOURCE}")/.." && pwd -P)"

go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen \
  schemapatch:manifests="${THIS_REPO_ABSOLUTE}/manifests" \
  paths="${THIS_REPO_ABSOLUTE}/pkg/apis/migration/v1alpha1" \
  output:dir="${THIS_REPO_ABSOLUTE}/manifests"

go run ./vendor/k8s.io/code-generator/cmd/client-gen \
  --output-package "${THIS_REPO}/pkg/clients" \
  --clientset-name="clientset" \
  --input-base="${THIS_REPO}" \
  --input="pkg/apis/migration/v1alpha1" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt"

go run ./vendor/k8s.io/code-generator/cmd/lister-gen \
  --output-package "${THIS_REPO}/pkg/clients/lister" \
  --input-dirs="${API_PKG}" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt"

go run ./vendor/k8s.io/code-generator/cmd/informer-gen \
  --output-package "${THIS_REPO}/pkg/clients/informer" \
  --input-dirs="${API_PKG}" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt" \
  --single-directory\
  --versioned-clientset-package "${THIS_REPO}/pkg/clients/clientset" \
  --listers-package "${THIS_REPO}/pkg/clients/lister"

go run ./vendor/k8s.io/code-generator/cmd/deepcopy-gen \
  --input-dirs="${API_PKG}" \
  --output-file-base="zz_generated.deepcopy" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt"
