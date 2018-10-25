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

# Sanity check.
# Note that the log inidicates that the kubectl in the test driver is v1.10.7
kubectl version

# TODO: in the real test, create objects here.

# Change the apiserver manifest to change the storage version.
gcloud compute --project "${PROJECT}" ssh --zone "${KUBE_GCE_ZONE}" "${CLUSTER_NAME}-master" --command \
"sudo sed -i \"s/--v=/--storage-versions=apps\/v1beta2 --v=/\" /etc/kubernetes/manifests/kube-apiserver.manifest"

wait-for-apiserver 

# TODO: in the real test, remove this block.
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

# TODO: run the migrator

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
