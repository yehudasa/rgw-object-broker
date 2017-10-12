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

---

## Package Structure

The CNS Object Broker is composed of two components: the broker itself, and an http server.
Both components are located under `cns-object-broker/pkg/` in their respective packages.  
The server receives REST calls and translates them into broker methods.  
The server also returns broker json responses relative to the method called.
For instance, a `CreateServiceInstanceRequest` has an accompanying `CreateServiceInstanceResponse`.

---

## Testing a WIP CNS Object Broker image

To test a broker built from a working branch, the tag must be known to helm.
There are two ways to do this.

**NOTE: It is not necessary to recreate any other component of the cluster.**


### Option 1: Helm CLI

Simply tear down the existing CNS Object Broker and install it again while invoking the `--set` helm option.

```
# helm delete --purge broker
# helm install ./chart/ --set image=gcr.io/openshift-gce-devel/cns-obj-broker:<tag> --name broker --namespace broker
```

This method require that `--set` be invoked each time the CNS Object Broker is installed.

### Option 2: Edit the Chart

Open [chart/values.yaml](../chart/values.yaml) for editing.
Change the `image` value to the image tag you would like to test.
Here, the `image` would be assigned `gcr.io/openshift-gce-devel/cns-obj-broker:<commit hash>`

```yaml
# Default values for CNS Object Broker
# Image to use.  Default is :v1.  
# Specify
#   --set image=gcr.io/openshift-gce-devel/cns-obj-broker:<tag>
# with `helm install` to use a non-default version. Useful for running WIP images.
image: gcr.io/openshift-gce-devel/cns-obj-broker:<image tag to test>
# ImagePullPolicy; valid values are "IfNotPresent", "Never", and "Always"
imagePullPolicy: Always
```

Once the desired image is defined, simply reinstall the broker with Helm.

Delete a running broker:

`# helm delete --purge broker`

And install the broker:

`# helm install ./chart/ --name broker --namespace=broker`

### Debugging

The CNS Object Broker log are accessible through `kubectl`.  Shell into the GCE cluster and execute:

`# kubectl -n broker logs <broker pod>`
