# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/kcl-lang/flux-kcl-controller:v0.1.0
# Produce CRDs that work back to Kubernetes 1.16
CRD_OPTIONS ?= crd:crdVersions=v1
SOURCE_VER ?= $(shell go list -m all | grep github.com/fluxcd/source-controller/api | awk '{print $$2}')

# Use the same version of SOPS already referenced on go.mod
SOPS_VER := $(shell go list -m all | grep github.com/getsops/sops | awk '{print $$2}')

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Allows for defining additional Go test args, e.g. '-tags integration'.
GO_TEST_ARGS ?=
# Allows for defining additional Docker buildx arguments, e.g. '--push'.
BUILD_ARGS ?=
# Architectures to build images for.
BUILD_PLATFORMS ?= linux/amd64
# Architecture to use envtest with
ENVTEST_ARCH ?= amd64
# Paths to download the CRD dependencies at.
GITREPO_CRD ?= config/crd/bases/gitrepositories.yaml
BUCKET_CRD ?= config/crd/bases/buckets.yaml
OCIREPO_CRD ?= config/crd/bases/ocirepositories.yaml

# Keep a record of the version of the downloaded source CRDs. It is used to
# detect and download new CRDs when the SOURCE_VER changes.
SOURCE_CRD_VER=bin/.src-crd-$(SOURCE_VER)

PACKAGE ?= github.com/kcl-lang/flux-kcl-controller
INPUT_DIRS := $(PACKAGE)/api/v1alpha1
BOILERPLATE_PATH := ${PWD}/hack/boilerplate.go.txt

# API (doc) generation utilities
GEN_API_REF_DOCS_VERSION ?= e327d0730470cbd61b06300f81c5fcf91c23c113

all: manager

# Run tests
KUBEBUILDER_ASSETS?="$(shell $(ENVTEST) --arch=$(ENVTEST_ARCH) use -i $(ENVTEST_KUBERNETES_VERSION) --bin-dir=$(ENVTEST_ASSETS_DIR) -p path)"
test: generate tidy fmt vet manifests download-crd-deps install-envtest $(SOPS)
	KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) go test ./... -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./cmd/main.go

# Delete previously downloaded CRDs and record the new version of the source
# CRDs.
$(SOURCE_CRD_VER):
	rm -f bin/.src-crd*
	$(MAKE) cleanup-crd-deps
	if ! test -d "bin"; then mkdir -p bin; fi
	touch $(SOURCE_CRD_VER)

# Download the CRDs the controller depends on FluxCD GitRepo, OCIRepo and Bucket.
download-crd-deps: $(SOURCE_CRD_VER)
	curl -s https://raw.githubusercontent.com/fluxcd/source-controller/${SOURCE_VER}/config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml -o $(GITREPO_CRD)
	curl -s https://raw.githubusercontent.com/fluxcd/source-controller/${SOURCE_VER}/config/crd/bases/source.toolkit.fluxcd.io_buckets.yaml -o $(BUCKET_CRD)
	curl -s https://raw.githubusercontent.com/fluxcd/source-controller/${SOURCE_VER}/config/crd/bases/source.toolkit.fluxcd.io_ocirepositories.yaml -o $(OCIREPO_CRD)

# Delete the downloaded CRD dependencies.
cleanup-crd-deps:
	rm -f $(GITREPO_CRD) $(BUCKET_CRD) $(OCIREPO_CRD)

# Install CRDs into a cluster
install: manifests
	kustomize build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests
	kustomize build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	cd config/manager && kustomize edit set image kcl-controller=${IMG}
	kustomize build config/default | kubectl apply -f -

# Deploy controller dev image in the configured Kubernetes cluster in ~/.kube/config
dev-deploy: manifests
	mkdir -p config/dev && cp config/default/* config/dev
	cd config/dev && kustomize edit set image fluxcd/kustomize-controller=${IMG}
	kustomize build config/dev | kubectl apply -f -
	rm -rf config/dev

# Undeploy controller in the configured Kubernetes cluster in ~/.kube/config
undeploy:
	cd config/manager && kustomize edit set image kcl-controller=${IMG}
	kustomize build config/default | kubectl delete -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen deepcopy-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=source-reader webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	$(DEEPCOPY_GEN) --input-dirs=$(INPUT_DIRS) --output-file-base=zz_generated.deepcopy --go-header-file=${BOILERPLATE_PATH}

# Run go tidy to cleanup go.mod
tidy:
	rm -f go.sum; go mod tidy

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Run go build
build:
	go build ./...

# Generate code
generate: controller-gen deepcopy-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	$(DEEPCOPY_GEN) --input-dirs=$(INPUT_DIRS) --output-file-base=zz_generated.deepcopy --go-header-file=${BOILERPLATE_PATH}

# Build the docker image
docker-build:
	docker buildx build \
	--platform=$(BUILD_PLATFORMS) \
	-t ${IMG} \
	--load \
	${BUILD_ARGS} .

# Push the docker image
docker-push:
	docker push ${IMG}

manifests-release:
	mkdir -p ./build
	mkdir -p config/release-tmp && cp config/release/* config/release-tmp
	cd config/release-tmp && kustomize edit set image kcl-lang/kcl-controller=${IMG}
	kustomize build config/release-tmp > ./build/kcl-controller.deployment.yaml
	rm -rf config/release-tmp

# Find or download controller-gen
CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
CONTROLLER_GEN_VERSION ?= v0.16.1
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION))

DEEPCOPY_GEN = $(shell pwd)/bin/deepcopy-gen
DEEPCOPY_GEN_VERSION ?= v0.29.0
.PHONY: deepcopy-gen
deepcopy-gen: ## Download controller-gen locally if necessary.
	$(call go-install-tool,$(DEEPCOPY_GEN),k8s.io/code-generator/cmd/deepcopy-gen@$(DEEPCOPY_GEN_VERSION))

ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
ENVTEST_KUBERNETES_VERSION?=latest
install-envtest: setup-envtest
	mkdir -p ${ENVTEST_ASSETS_DIR}
	$(ENVTEST) use $(ENVTEST_KUBERNETES_VERSION) --arch=$(ENVTEST_ARCH) --bin-dir=$(ENVTEST_ASSETS_DIR)

SOPS = $(GOBIN)/sops
$(SOPS): ## Download latest sops binary if none is found.
	$(call go-install-tool,$(SOPS),github.com/getsops/sops/v3/cmd/sops@$(SOPS_VER))

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
setup-envtest: ## Download envtest-setup locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

# go-install-tool will 'go install' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef
