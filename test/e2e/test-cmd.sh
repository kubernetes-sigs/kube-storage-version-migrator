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

TESTFILE="v1beta2-controllerrevision.proto"

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

# $1 should be the etcd container's hash.
# $2 should be the expected controllerrevisions's storage version.
verify-version()
{
  version=$(gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
    "docker exec $1 /bin/sh -c \"ETCDCTL_API=3 etcdctl get /registry/controllerrevisions/default/sample\" | grep -a apps")
  # Remove the trailing non-printable character. The data is encoded in proto, so
  # it has non-printable characters.
  version=$(tr -dc '[[:print:]]' <<< "${version}")
  echo "${version}"
  if [[ "${version}" = *"$2"* ]]; then
    echo "Version check passed, expected $2, got ${version}"
    return 0
  else
    echo "Version check failed, expected $2, got ${version}!"
    return 1
  fi
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

ls ~/.kube/
echo "user is"
echo $USER
echo "which ginkgo"
which ginkgo
kubectl config view



sleep 6000

# Sanity check.
# Note that the log inidicates that the kubectl in the test driver is v1.10.7
kubectl version

# This is to enable docker push. Running it here to fail early.
gcloud auth configure-docker

# TODO: Test more types of resources.

# STEP 1: create an object encoded in a non-default storage version. We cannot
# create the object via the apiserver, because apiserver always encode the
# object to the default storage version before storing in etcd.

# Copy the pre-made proto file of the object to the master machine.
user_name=$(gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command "whoami")
gcloud compute scp "${MIGRATORROOT}/test/e2e/${TESTFILE}" "${user_name}@${CLUSTER_NAME}-master:~/" --project "${PROJECT}" --zone "${KUBE_GCE_ZONE}"

# Get the etcd container ID.
result=$(gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
  "docker ps")
etcd_container=$(echo "${result}" | grep "etcd-server-${CLUSTER_NAME}-master" | grep -v pause | cut -d ' ' -f 1)

# Copy the proto file to the etcd container
gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
  "docker cp ${TESTFILE} ${etcd_container}:/"

# Create the object via etcdctl
gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
  "docker exec ${etcd_container} /bin/sh -c \"cat /${TESTFILE} | ETCDCTL_API=3 etcdctl put /registry/controllerrevisions/default/sample\""

#TODO: remove
# Verify that the ControllerRevision is encoded as apps/v1beta2.
verify-version "${etcd_container}" "apps/v1beta2"

# Validate that the creation via etcdctl is successful
kubectl get controllerrevisions sample --namespace=default

# STEP 2: build and deploy the migrator, wait for its completion.

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

# STEP 3: verify the object has been migrated.

# Verify that the ControllerRevision is encoded as apps/v1, the default storage
# version, in etcd.
verify-version "${etcd_container}" "apps/v1"
