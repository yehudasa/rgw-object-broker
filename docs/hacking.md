# Hacking on the CNS Object Broker

The CNS Object Broker conforms to the [Open Service Broker API](https://github.com/openservicebrokerapi/servicebroker/blob/v2.13/spec.md).  Its RESTful interface implements handles for

- creating and removing a service instance
- creating and removing a service binding (known as "service instance credential" in Service-Catalog)
- returning the json formatted Catalog

## About the Makefile

Golang projects typically don't require a Makefile.
The inclusion of one here is to bring compiling, Docker image building and deployment into a single CLI.
The Makefile will:
- compile a binary of the CNS Object Broker into the `cns-object-broker/bin/` directory.
- create a temporary directory `cns-object-broker/build/tmp/` in which to build the Docker image.
- push the docker image to the registry defined in `$(REGISTRY)`.
- push the image by tagging it with `$(VERSION)` and pushing it to `$(REGISTRY)`.

Image tags are created pseudo-uniquely by appending the hash of the current git commit.
The intent is to enable parallel testing of different working branches when using the same docker registry.  Each developer can build and push a Docker image unique to their branch without stepping on other developers' images.
When changes are merged into master, a specially tagged image can be created.  
This versioned image will be identifiable by a version tag rather than a git commit tag.

E.G.
- Git commit:   `gcr.io/openshift-gce-devel/cns-obj-broker:23ac9fc-dirty`
- Versioned:    `gcr.io/openshift-gce-devel/cns-obj-broker:v1`

### Make Commands

- `all`: Triggers `clean`, `build` and `image` make targets

- `clean`: Deletes the binary and temporary folder created by `build` and `image`.

- `build`: Compiles the broker binary into the `cns-object-broker/bin` directory.

- `image`: Copies the binary and Dockerfile into `cns-object-broker/build/tmp`, then builds the Docker container image with the git commit tag.

- `push`: Pushes the image whose tag matches the current abbreviated git commit to `$(REGISTRY)`

- `release`: Tags the image whose tag matches the current abbreviated git commit with `$(VERSION)` and pushes it to `$(REGSITRY)`

## Package Structure

The CNS Object Broker is composed of two components: the broker itself, and an http server.
Both components are located under `cns-object-broker/pkg/`.
