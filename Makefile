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

REGISTRY ?= gcr.io/$(shell gcloud config get-value project)
STAGING_REGISTRY := gcr.io/k8s-staging-storage-migrator
VERSION ?= v0.1
NAMESPACE ?= kube-system
DELETE ?= "gcloud container images delete"
COMPONENTS = initializer migrator trigger

.PHONY: test
test:
	go test ./pkg/...

.PHONY: all
all:
ifeq ($(WHAT),)
	go install sigs.k8s.io/kube-storage-version-migrator/cmd/...
else
	go install sigs.k8s.io/kube-storage-version-migrator/$(WHAT)
endif

.PHONY: all-images
all-images: $(COMPONENTS:%=image-%)
image-%:
	docker build --no-cache -t $(REGISTRY)/storage-version-migration-$*:$(VERSION) --file cmd/$*/Dockerfile .

.PHONY: e2e-test
e2e-test:
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go test -c -o ./test/e2e/e2e.test ./test/e2e

.PHONY: local-manifests
local-manifests:
	mkdir -p manifests.local
	cp manifests/* manifests.local/
	find ./manifests.local -type f -exec sed -i -e "s|REGISTRY|$(REGISTRY)|g" {} \;
	find ./manifests.local -type f -exec sed -i -e "s|VERSION|$(VERSION)|g" {} \;
	find ./manifests.local -type f -exec sed -i -e "s|NAMESPACE|$(NAMESPACE)|g" {} \;

.PHONY: push-all
push-all: $(COMPONENTS:%=push-%)
push-%: image-%
	docker push $(REGISTRY)/storage-version-migration-$*:$(VERSION)

.PHONY: release-staging release-alias-tag
release-staging: ## Builds and push container images to the staging bucket.
	REGISTRY=$(STAGING_REGISTRY) $(MAKE) push-all release-alias-tag

.PHONY: release-alias-tag
release-alias-tag: $(COMPONENTS:%=release-alias-tag-%)
release-alias-tag-%: # Adds the tag to the last build tag. BASE_REF comes from the cloudbuild.yaml
	gcloud container images add-tag --quiet $(REGISTRY)/storage-version-migration-$*:$(VERSION) $(REGISTRY)/storage-version-migration-$*:$(BASE_REF)

.PHONY: delete-all-images
delete-all-images: $(COMPONENTS:%=delete-image-%)
delete-image-%:
	eval "$(DELETE) $(REGISTRY)/storage-version-migration-$*:$(VERSION)"

.PHONY: clean
clean:
	$(RM) -r _output
	$(RM) -r manifests.local
	$(RM) test/e2e/e2e.test
