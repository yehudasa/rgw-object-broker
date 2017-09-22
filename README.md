# CNS Object Broker
## Utilize Kubernetes Service-Catalog to dynamically provision CNS Object Storage.

## WARNING!
The work in this project is a proof of concept and not intended for anything
more than that.

## Overview
A core feature of the Kubernetes system is the ability to provision a diverse
offering of block and file storage on demand.  This project seeks to demonstrate
that by using the Kubernetes-Incubator's Service-Catalog, it is now also possible
to bring this dynamic provisioning to S3 object storage.  This broker is designed
to be used with Gluster-Kubernetes S3.

See our detailed [command flow diagram](./docs/Service-Catalog-Published.png) for
 an overview of the system.

### What It Does
This broker implements OpenServiceBroker methods for creating, connecting to, and destroying
object buckets.
- For each new Service Instance, a new, unquiely named bucket is created.
- For each binding to a Service Instance, a Secret is generated with the
coordinates and credentials of the Service Instance's associated bucket.
- Deleting a Service Instance destroys the associated bucket.
- Deleting a Binding deletes the secret.  Nothing is done on the broker side.  This
is because the broker does not perform any actions on Bind, so there is nothing to
undo.

## Installation

### Dependencies
- [Google SDK](https://cloud.google.com/sdk/) (`gcloud`): To access GCP via cli.
- [Kubernetes](https://github.com/kubernetes/kubernetes): to run local K8s cluster
- [Gluster-Kubernetes Deploy Script](https://github.com/copejon/gk-cluster-deploy): to deploy GCP K8s cluster with Gluster-Kubernetes Object Storage and CNS Object Broker
- Kubernetes-Incubator [Service-Catalog](https://github.com/kubernetes-incubator/service-catalog): to provide Service-Catalog helm chart
- Kubernetes [Helm](https://github.com/kubernetes/helm#install): to install Service-Catalog chart

### Assumptions
- Gk-cluster-deploy was written and tested on Fedora 25 and RHEL 7.
- A [Google Cloud Platform](https://cloud.google.com/compute/) account.

### Topology
There are two primary systems that make up this demonstration.  They are the Broker
and it's colocated CNS Object store.  They fill the role of our micro-service provider.
The second system will be the locally running Kubernetes cluster on which with Service-Catalog is deployed.  This cluster will be our micro-server client.  Please refer to the [command flow diagram](./docs/Service-Catalog-Published.png) for a more in depth look at these systems.

## Setup
#### Step 0: Preparing environment
- Install Google SDK and add `gcloud` to `PATH`.  You must have an active Google
Cloud Platform account.
- Clone CNS Object Broker
- Clone Kubernetes
- Configure `gcloud` user, zone, and project.  These are detected by the deployment script and required to setup. Alternatively, you can specify these values as environment variables or inline.  At a minimum, required variables are:

```
GCP_USER=<gcp user name>
GCP_ZONE=<gcp zone>
GCP_REGION=<gcp region>
```

#### Step 1: Deploy Gluster-Kubernetes cluster in GCP
This step sets up the **External Service Provider** portion of our topology (see [diagram](./docs/Service-Catalog-Published.png)).

To kick off deployment, run `gk-cluster-deploy/deploy/cluster/gk-up.sh`.  The script has a number of configurable
variables relative to GCP Instance settings.  These can be defined inline with `gk-up.sh` or as environment variables.  To run the script as fire and forget, pass `-y` to skip configuration review.

Runtime take around 5 to 10 minutes.  Go get some coffee.

When deployment completes, commands to ssh into the master node and the URL of the CNS Object Broker will be output.  Make a note of the URL especially.

#### Step 2: Spin up a Local Kubernetes Cluster
This step sets up the **K8s Cluster** portion of our topology (see [diagram](./docs/Service-Catalog-Published.png)).

Create a local Kubernetes cluster:
```
KUBE_ENABLE_CLUSTER_DNS=true kubernetes/hack/local-up-cluster.sh
```

*In a separate terminal:*
