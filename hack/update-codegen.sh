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

THIS_REPO="github.com/kubernetes-sigs/kube-storage-version-migrator"
API_PKG="${THIS_REPO}/pkg/apis/migration/v1alpha1"
# Absolute path to this repo
THIS_REPO_ABSOLUTE="$(cd "$(dirname "${BASH_SOURCE}")/.." && pwd -P)"
WORK_DIR=`mktemp -d`

function cleanup {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

# Install code generators
mkdir -p ${WORK_DIR}/go/src/k8s.io
pushd ${WORK_DIR}/go/src/k8s.io
git clone git@github.com:kubernetes/code-generator.git
popd
pushd ${WORK_DIR}/go/src/k8s.io/code-generator
# The version needs to match the one in Gopkg.toml
git checkout kubernetes-1.15.0-alpha.0
GOPATH=${WORK_DIR}/go/ go install k8s.io/code-generator/cmd/lister-gen
GOPATH=${WORK_DIR}/go/ go install k8s.io/code-generator/cmd/informer-gen
GOPATH=${WORK_DIR}/go/ go install k8s.io/code-generator/cmd/client-gen
GOPATH=${WORK_DIR}/go/ go install k8s.io/code-generator/cmd/deepcopy-gen
popd

${WORK_DIR}/go/bin/client-gen \
  --output-package "${THIS_REPO}/pkg/clients" \
  --clientset-name="clientset" \
  --input-base="${THIS_REPO}" \
  --input="pkg/apis/migration/v1alpha1" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt"

${WORK_DIR}/go/bin/lister-gen \
  --output-package "${THIS_REPO}/pkg/clients/lister" \
  --input-dirs="${API_PKG}" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt"

${WORK_DIR}/go/bin/informer-gen \
  --output-package "${THIS_REPO}/pkg/clients/informer" \
  --input-dirs="${API_PKG}" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt" \
  --single-directory\
  --versioned-clientset-package "${THIS_REPO}/pkg/clients/clientset" \
  --listers-package "${THIS_REPO}/pkg/clients/lister"

${WORK_DIR}/go/bin/deepcopy-gen \
  --input-dirs="${API_PKG}" \
  --output-file-base="zz_generated.deepcopy" \
  --go-header-file "${THIS_REPO_ABSOLUTE}/hack/boilerplate/boilerplate.generatego.txt"
