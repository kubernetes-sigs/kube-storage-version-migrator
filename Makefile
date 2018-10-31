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

.PHONY: test
test:
	go test ./...

.PHONY: all
all:
ifeq ($(WHAT),)
	go install github.com/kubernetes-sigs/kube-storage-version-migrator/cmd/...
else
	go install github.com/kubernetes-sigs/kube-storage-version-migrator/$(WHAT)
endif

.PHONY: all-containers
all-containers:
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cmd/initializer/initializer ./cmd/initializer
	docker build --no-cache -t $(REGISTRY)/storage-version-migration-initializer:$(VERSION) cmd/initializer
	rm cmd/initializer/initializer
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cmd/migrator/migrator ./cmd/migrator
	docker build --no-cache -t $(REGISTRY)/storage-version-migration-migrator:$(VERSION) cmd/migrator
	rm cmd/migrator/migrator

.PHONY: local-manifests
local-manifests:
	mkdir -p manifests.local
	cp manifests/* manifests.local/
	find ./manifests.local -type f -exec sed -i -e "s|REGISTRY|$(REGISTRY)|g" {} \;
	find ./manifests.local -type f -exec sed -i -e "s|VERSION|$(VERSION)|g" {} \;

.PHONY: all-containers
push-all: all-containers
	docker push $(REGISTRY)/storage-version-migration-initializer:$(VERSION)
	docker push $(REGISTRY)/storage-version-migration-migrator:$(VERSION)

.PHONY: clean
clean:
	rm  -r ./manifests.local
