#!/usr/bin/env bash

# Copyright 2019 The Kubernetes Authors.
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

# This script tests automatically started migrations with the triggering
# controller and the migrator.

set -o errexit
set -o nounset
set -o pipefail

MIGRATOR_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null && pwd )"/../..
KUBE_ROOT="${MIGRATOR_ROOT}/kubernetes"
source "${KUBE_ROOT}/cluster/common.sh"
source "${KUBE_ROOT}/hack/lib/init.sh"
# Find the ginkgo binary build as part of the release.
ginkgo=$(kube::util::find-binary "ginkgo")

# This is to enable docker push. Running it here to fail early.
gcloud auth configure-docker

cleanup() {
  if [[ -z "${REGISTRY}" ]]; then
    return
  fi
  pushd "${MIGRATOR_ROOT}"
    echo "===== migrator logs"
    kubectl logs --namespace=kube-system deployment/migrator || true
    echo "===== trigger logs"
    kubectl logs --namespace=kube-system deployment/trigger || true
    echo "===== storageversionmigrations"
    kubectl get storageversionmigrations.migration.k8s.io -o yaml
    echo "===== storagestates"
    kubectl get storagestates.migration.k8s.io -o yaml
    echo "Deleting images"
    make delete-all-images
    echo "Deleting images successfully"
  popd
}

trap cleanup EXIT

# Build and deploy the migrator, wait for its completion.
pushd "${MIGRATOR_ROOT}"
  export REGISTRY="gcr.io/${PROJECT}"
  echo "REGISTRY=${REGISTRY}"
  commit=$(git rev-parse --short HEAD)
  export VERSION="v${commit}"
  echo "VERSION=${VERSION}"
  make local-manifests
  make push-all
popd

pushd "${MIGRATOR_ROOT}"
  make e2e-test
  "${ginkgo}" -v "$@" "${MIGRATOR_ROOT}/test/e2e/e2e.test"
popd
