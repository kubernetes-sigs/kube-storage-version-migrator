#!/usr/bin/env bash

# Copyright 2020 The Kubernetes Authors.
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
set -o xtrace

MIGRATORROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null && pwd )"/../..
REGISTRY=""
VERSION=""

CLUSTER_NAME="kind-test-e2e-55"

failure() {
  local lineno=$1
  local msg=$2
  echo "Failed at $lineno: $msg"
}

trap 'failure ${LINENO} "$BASH_COMMAND"' ERR

SCRIPT_PATH="$(dirname "$(readlink -f "$0")")"
KINDER_ROOT_PATH="$(readlink -f "${SCRIPT_PATH}/../../../../k8s.io/kubeadm/kinder/")"

# TODO: use gcp
docker login -u roycaihwtest -p kubesmtest

pushd "${MIGRATORROOT}"
export REGISTRY="roycaihwtest"
echo "REGISTRY=${REGISTRY}"
commit=$(git rev-parse --short HEAD)
export VERSION="v${commit}"
echo "VERSION=${VERSION}"
make local-manifests
make push-all
popd

# build kinder
pushd "${KINDER_ROOT_PATH}"
echo "Building kinder..."
GO111MODULE=on go build
popd

export PATH="${PATH}:${KINDER_ROOT_PATH}"

# TODO(roycaihw): automate or document how to build the altered image
docker pull roycaihw/kindest-node:test-sv-crd

kinder create cluster --image roycaihw/kindest-node:test-sv-crd --control-plane-nodes 3 --name "${CLUSTER_NAME}" --worker-nodes 0
kinder do kubeadm-init --name "${CLUSTER_NAME}" --copy-certs=auto --loglevel=debug
export KUBECONFIG="$(kinder get kubeconfig-path --name="${CLUSTER_NAME}")"

# Sanity check.
kubectl version

kinder do kubeadm-join --name "${CLUSTER_NAME}" --loglevel=debug

pushd "${MIGRATORROOT}/manifests.local"
kubectl apply -f storage_migration_crd.yaml
kubectl apply -f storage_state_crd.yaml
kubectl apply -f namespace-rbac.yaml
kubectl apply -f trigger.yaml
kubectl apply -f migrator.yaml
popd

kinder do kubeadm-upgrade --upgrade-version v1.20.0-beta.2.67+b0adc0a51238ee --only-node "${CLUSTER_NAME}"-control-plane-1 --name "${CLUSTER_NAME}" --loglevel=debug

kinder do kubeadm-upgrade --upgrade-version v1.20.0-beta.2.67+b0adc0a51238ee --only-node "${CLUSTER_NAME}"-control-plane-2 --name "${CLUSTER_NAME}" --loglevel=debug

kinder do kubeadm-upgrade --upgrade-version v1.20.0-beta.2.67+b0adc0a51238ee --only-node "${CLUSTER_NAME}"-control-plane-3 --name "${CLUSTER_NAME}" --loglevel=debug

# wait for storage version GC
sleep 180

kubectl get pods -A
kubectl get storageversionmigrations
kubectl get storageversions
kubectl get storageversions apiextensions.k8s.io.customresourcedefinitions -oyaml

CRDNAME=$(kubectl get storageversionmigrations | grep customresourcedefinitions.apiextensions.k8s.io | grep -o '^\S*')
VERSION=$(kubectl get storagestates customresourcedefinitions.apiextensions.k8s.io -o jsonpath='{.status.currentStorageVersionHash}')
if [ "$VERSION" != "apiextensions.k8s.io/v1" ]; then
  echo "has wrong version after upgrade master 3: $VERSION"
  exit 1
fi

kubectl get storageversionmigrations "$CRDNAME" -oyaml

# TODO: evaluate object in etcd
echo "succeeded"

kinder do kubeadm-reset --name "${CLUSTER_NAME}" --loglevel=debug
kinder delete cluster --name "${CLUSTER_NAME}" --loglevel=debug
