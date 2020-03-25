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

REGISTRY ?= gcr.io/google-containers
VERSION ?= v0.1
NAMESPACE ?= kube-system
DELETE ?= "gcloud container images delete"

.PHONY: test
test:
	GO111MODULE=on go test -mod=vendor ./pkg/...

.PHONY: all
all:
ifeq ($(WHAT),)
	go install sigs.k8s.io/kube-storage-version-migrator/cmd/...
else
	go install sigs.k8s.io/kube-storage-version-migrator/$(WHAT)
endif

.PHONY: all-containers
all-containers:
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -mod=vendor -ldflags "-X sigs.k8s.io/kube-storage-version-migrator/pkg/version.VERSION=$(VERSION)" -a -installsuffix cgo -o cmd/initializer/initializer ./cmd/initializer
	docker build --no-cache -t $(REGISTRY)/storage-version-migration-initializer:$(VERSION) cmd/initializer
	rm cmd/initializer/initializer
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -mod=vendor -ldflags "-X sigs.k8s.io/kube-storage-version-migrator/pkg/version.VERSION=$(VERSION)" -a -installsuffix cgo -o cmd/migrator/migrator ./cmd/migrator
	docker build --no-cache -t $(REGISTRY)/storage-version-migration-migrator:$(VERSION) cmd/migrator
	rm cmd/migrator/migrator
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -mod=vendor -ldflags "-X sigs.k8s.io/kube-storage-version-migrator/pkg/version.VERSION=$(VERSION)" -a -installsuffix cgo -o cmd/trigger/trigger ./cmd/trigger
	docker build --no-cache -t $(REGISTRY)/storage-version-migration-trigger:$(VERSION) cmd/trigger
	rm cmd/trigger/trigger

.PHONY: e2e-test
e2e-test:
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go test -mod=vendor -c -o ./test/e2e/e2e.test ./test/e2e

.PHONY: local-manifests
local-manifests:
	mkdir -p manifests.local
	cp manifests/* manifests.local/
	find ./manifests.local -type f -exec sed -i -e "s|REGISTRY|$(REGISTRY)|g" {} \;
	find ./manifests.local -type f -exec sed -i -e "s|VERSION|$(VERSION)|g" {} \;
	find ./manifests.local -type f -exec sed -i -e "s|NAMESPACE|$(NAMESPACE)|g" {} \;

.PHONY: all-containers
push-all: all-containers
	docker push $(REGISTRY)/storage-version-migration-initializer:$(VERSION)
	docker push $(REGISTRY)/storage-version-migration-migrator:$(VERSION)
	docker push $(REGISTRY)/storage-version-migration-trigger:$(VERSION)

.PHONY: delete-all-images
delete-all-images:
	eval "$(DELETE) $(REGISTRY)/storage-version-migration-initializer:$(VERSION)"
	eval "$(DELETE) $(REGISTRY)/storage-version-migration-migrator:$(VERSION)"
	eval "$(DELETE) $(REGISTRY)/storage-version-migration-trigger:$(VERSION)"

.PHONY: clean
clean:
	rm  -r ./manifests.local
