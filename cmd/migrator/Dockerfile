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
FROM golang:1.19.2 as builder
WORKDIR /go/src/sigs.k8s.io/kube-storage-version-migrator
COPY . .
ARG VERSION=v0.1.0-dev
RUN CGO_ENABLED=0 go build -ldflags "-X sigs.k8s.io/kube-storage-version-migrator/pkg/version.VERSION=$VERSION" -a -installsuffix cgo ./cmd/migrator

FROM gcr.io/distroless/static:latest
COPY --from=builder /go/src/sigs.k8s.io/kube-storage-version-migrator/migrator /migrator
ENTRYPOINT ["/migrator", "--v=2"]
