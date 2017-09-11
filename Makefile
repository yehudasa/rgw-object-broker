REPO_ROOT=$(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR=$(REPO_ROOT)/bin
BIN_TARGET=cns-obj-broker
PKG_DIR=$(REPO_ROOT)/main
BUILD_DIR=$(REPO_ROOT)/build

# DOCKER TAG VARS
REGISTRY=gcr.io/openshift-gce-devel
IMAGE=cns-obj-broker
VERSION=$(shell git describe --always --abbrev=7 --dirty)

.PHONY: broker image release push clean
all: clean broker image 

# Compile broker binary
broker: $(BIN_DIR)/$(BIN_TARGET)
$(BIN_DIR)/$(BIN_TARGET): $(PKG_DIR)/main.go
	go build -i -o $(BIN_DIR)/$(BIN_TARGET) $(PKG_DIR)/main.go

# build the broker image
image: $(BUILD_DIR)/Dockerfile 
	$(eval TEMP_BUILD_DIR=$(BUILD_DIR)/tmp)
	mkdir -p $(TEMP_BUILD_DIR)
	cp $(BIN_DIR)/$(BIN_TARGET) $(TEMP_BUILD_DIR)
	cp $(BUILD_DIR)/Dockerfile $(TEMP_BUILD_DIR)
	docker build -t $(IMAGE) $(TEMP_BUILD_DIR)
	docker tag $(IMAGE) $(REGISTRY)/$(IMAGE):$(VERSION)
	rm -rf $(TEMP_BUILD_DIR)

# push IMAGE:VERSION. Intended to push wip broker built from non-master branch.
push:
	gcloud docker -- push $(REGISTRY)/$(IMAGE):$(VERSION)

# push IMAGE:latest. Intended to release stable image built from master branch.
release:
	gcloud docker -- push $(REGISTRY)/$(IMAGE)

clean:
	rm -rf $(BIN_DIR)/*
	rm -rf $(BUILD_DIR)/tmp
