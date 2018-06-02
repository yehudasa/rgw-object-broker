REPO_ROOT=$(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR=$(REPO_ROOT)/bin
BIN_TARGET=rgw-obj-broker
PKG_DIR=$(REPO_ROOT)/main
BUILD_DIR=$(REPO_ROOT)/build
BUILD_COUNT_FILE=$(REPO_ROOT)/scripts/.build

# DOCKER TAG VARS
REGISTRY=172.17.8.1:5000
# gcr.io/openshift-gce-devel
IMAGE=rgw-obj-broker
DIRTY_HASH=$(shell git describe --always --abbrev=7 --dirty)-$(shell cat $(BUILD_COUNT_FILE))
VERSION=v1

.PHONY: broker image release push clean
all: broker image 

# Compile broker binary
broker: $(BIN_DIR)/$(BIN_TARGET)

$(BIN_DIR)/$(BIN_TARGET): $(PKG_DIR)/main.go pkg/broker/broker.go
	go build -i -o $(BIN_DIR)/$(BIN_TARGET) $(PKG_DIR)/main.go

# build the broker image
image: $(BUILD_DIR)/Dockerfile 
	$(eval TEMP_BUILD_DIR=$(BUILD_DIR)/tmp)
	mkdir -p $(TEMP_BUILD_DIR)
	cp $(BIN_DIR)/$(BIN_TARGET) $(TEMP_BUILD_DIR)
	cp $(BUILD_DIR)/Dockerfile $(TEMP_BUILD_DIR)
	docker build -t $(IMAGE) $(TEMP_BUILD_DIR)
	docker tag $(IMAGE) $(REGISTRY)/$(IMAGE):$(DIRTY_HASH)
	# docker tag $(IMAGE) $(REGISTRY)/$(IMAGE):v1
	rm -rf $(TEMP_BUILD_DIR)

values: $(BUILD_COUNT_FILE) chart/values.yaml
	$(shell cat chart/values.yaml.template | sed s/{release}/$(DIRTY_HASH)/g | sed s/{registry}/$(REGISTRY)/g > chart/values.yaml)

# push IMAGE:$(DIRTY_HASH). Intended to push broker built from non-master / working branch.
push: broker image values
	# gcloud docker -- push $(REGISTRY)/$(IMAGE):$(DIRTY_HASH)
	docker push $(REGISTRY)/$(IMAGE):$(DIRTY_HASH)
	@echo ""
	@echo "-- Pushed image:"
	@echo ""
	@echo "        $(REGISTRY)/$(IMAGE):$(DIRTY_HASH)"
	@echo ""
	@echo "-- Be sure to update chart/values.yaml!"
	@echo ""

# push IMAGE:$(VERSION). Intended to release stable image built from master branch.
release:
	git fetch origin
ifneq ($(shell git rev-parse --abbrev-ref HEAD), master)
	$(error Release is intended to be run on master branch. Please checkout master and retry.)
endif
ifneq ($(shell git rev-list HEAD..origin/master --count), 0)
	$(error HEAD is behind origin/master -- $(shell git status -sb --porcelain))
endif
ifneq ($(shell git rev-list origin/master..HEAD --count), 0)
	$(error HEAD is ahead of origin/master --  $(shell git status -sb --porcelain))
endif
	docker tag $(IMAGE) $(REGISTRY)/$(IMAGE):$(VERSION)
	# gcloud docker -- push $(REGISTRY)/$(IMAGE)
	docker push $(REGISTRY)/$(IMAGE)

clean:
	rm -rf $(BIN_DIR)/*
	rm -rf $(BUILD_DIR)/tmp
