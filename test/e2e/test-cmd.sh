#!/usr/bin/env bash

# Copyright 2018 The Kubernetes Authors.
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

MIGRATORROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null && pwd )"/../..
REGISTRY=""
VERSION=""

# Retry 10 times with 10s interval to wait for the apiserver to come back.
function wait-for-apiserver()
{
  for count in {0..9}; do
    kubectl version && rc=$? || rc=$?
    if [ ${rc} -eq 0 ]; then
      return 0
    else
      sleep 10
    fi
  done
  return 1
}

function wait-for-migration()
{
  # wait for initialization
  for count in {0..9}; do
    tasks=$(kubectl get storageversionmigrations.migration.k8s.io --namespace=kube-storage-migration -o json | jq -r '.items | length') && rc=$? || rc=$?
    if [ ${rc} -ne 0 ]; then
      echo "retry after 10s"
      sleep 10
      continue
    fi
    if [ ${tasks} -eq 0 ]; then
      echo "no storageversionmigrations objects created yet, retry after 10s"
      sleep 10
    else
      echo "At least ${tasks} storageversionmigrations objects have been created"
      break
    fi
  done

  # wait for the migrations to complete
  for count in {0..9}; do
    # pending storageversionmigrations either have no status, or have
    # status.conditions that are not "Succeeded".
    pendings=$(kubectl get storageversionmigrations.migration.k8s.io --namespace=kube-storage-migration -o json | jq -r '.items[] | select((has("status") | not) or ([ .status.conditions[] | select(.type != "Succeeded" and .status == "True") ] | length !=0) ) | .metadata.namespace + "/" + .metadata.name')
    # Note that number=1 when pendings="".
    number=$(echo "${pendings}" | wc -l)
    if [ -z "${pendings}" ]; then
      return 0
    else
      echo "${number} migrations haven't succeeded yet"
      echo "They are:"
      echo "${pendings}"
      echo "retry after 10s"
      echo ""
      sleep 10
    fi
  done

  echo "Timed out waiting for migration to complete."
  echo "initializer logs:"
  kubectl logs --namespace=kube-storage-migration -l job-name=initializer || true
  echo "migrator logs:"
  kubectl logs --namespace=kube-storage-migration -l app=migrator || true
  return 1
} 

cleanup() {
  if [[ -z "${REGISTRY}" ]]; then
    return
  fi
  pushd "${MIGRATORROOT}"
    echo "Deleting images"
    make delete-all-images
    echo "Deleting images successfully"
  popd

  kubectl get storageversionmigrations.migration.k8s.io --namespace=kube-storage-migration -o json
}

trap cleanup EXIT

# Sanity check.
# Note that the log inidicates that the kubectl in the test driver is v1.10.7
kubectl version

# This is to enable docker push. Running it here to fail early.
gcloud auth configure-docker

# TODO: create more objects.
# STEP 1: create an object

# We use controller revision as the test subject because it has multiple
# versions, and no controller is going to write to it.
cat <<EOF | kubectl create --namespace=default -f -
apiVersion: apps/v1
kind: ControllerRevision
metadata:
  name: sample
data:
  Raw: a
revision: 1
EOF

kubectl get controllerrevisions sample --namespace=default

# STEP 2: change the apiserver configuration of --storage-versions.

# Change the apiserver manifest to change the storage version.
# TODO: Set the storage version based on the version of the apiserver. There is
# no guarantee that apps/v1beta2 is always supported.
gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
"sudo sed -i \"s/--v=/--storage-versions=apps\/v1beta2 --v=/\" /etc/kubernetes/manifests/kube-apiserver.manifest"

wait-for-apiserver 

# STEP 3: build and deploy the migrator, wait for its completion.

pushd "${MIGRATORROOT}"
export REGISTRY="gcr.io/${PROJECT}"
echo "REGISTRY=${REGISTRY}"
commit=$(git rev-parse --short HEAD)
export VERSION="v${commit}"
echo "VERSION=${VERSION}"
make local-manifests
make push-all
popd

pushd "${MIGRATORROOT}/manifests.local"
kubectl apply -f namespace-rbac.yaml
kubectl apply -f initializer.yaml
kubectl apply -f migrator.yaml
popd

wait-for-migration

# STEP 4: verify the object has been migrated.

# Verify the ControllerRevision is encoded as apps/v1beta2 in etcd.
result=$(gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
  "docker ps")
etcd_container=$(echo "${result}" | grep "etcd-server-${CLUSTER_NAME}-master" | grep -v pause | cut -d ' ' -f 1)

version=$(gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
  "docker exec ${etcd_container} /bin/sh -c \"ETCDCTL_API=3 etcdctl get /registry/controllerrevisions/default/sample\" | grep apps")
# Remove the trailing non-printable character. The data is encoded in proto, so
# it has non-printable characters.
version=$(tr -dc '[[:print:]]' <<< "${version}")
echo "${version}"

if [[ "${version}" = *"apps/v1beta2"* ]]; then
  echo "Succeeded!"
  exit 0
else
  echo "Failed, expected apps/v1beta2, got ${version}!"
  exit 1
fi
