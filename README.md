# CNS Object Broker

## WARNING!
The work in this project is a proof of concept and not intended for production.

## Utilize Kubernetes Service-Catalog to dynamically provision CNS Object Storage.

## Overview
A core feature of the Kubernetes system is the ability to provision block and file storage on demand.
This project seeks to demonstrate that by using the Kubernetes-Incubator's
[Service-Catalog](https://github.com/kubernetes-incubator/service-catalog) and the CNS Object Broker,
it is now possible to provision GlusterFS backed S3 buckets on demand.
This is accomplished using [Gluster-Kubernetes](https://github.com/jarrpa/gluster-kubernetes), the Service-Catalog, and the CNS Object Broker.  Gluster-Kubernetes provides the S3 interface and backing storage. Service-Catalog enables communication between a client Kubernetes cluster and service provider. The CNS Object Broker is the endpoint to which the Service-Catalog sends requests for services.

The CNS Object Broker handles requests to create and destroy S3 buckets and returns information required to consume them.
The CNS Object Broker is designed
to be used only with [Gluster-Kubernetes](https://github.com/jarrpa/gluster-kubernetes).
Our [command flow diagram](docs/diagram/control-diag.md) shows where Kubernetes, Service-Catalog,
and the (often external) service broker interact. Service-catalog terms used below are included in the diagram.



### Topology
There are two separate environments that make up this demonstration.
The first is a 4 node GCE cluster.  The CNS Object Broker and Gluster-Kubernetes are run here and represent the External Service Provider.
It should be noted that the Broker can be implemented to run anywhere.
This system will consist of *at least* 4 GCE instances.  **Each minion instance MUST have an additional raw block device.**

Our second system will be the locally running Kubernetes cluster on which with Service-Catalog is deployed.
This system will be run locally in a Kubernetes *all-in-one* cluster and is where consumers of our storage service will be located.
Please refer to the [command flow diagram](docs/diagram/control-diag.md) for a more in depth look at these systems.

### Nomenclature

Before going further, it's useful to define common terms used throughout the entire system.
The relationships between the different systems and objects can be seen in the [control flow diagram](docs/diagram/control-diag.md).
There are a number of naming collisions which can lead to some confusion.

#### External Service Provider

- *External Service Provider*

  For our purposes, an External Service Provider is any system that offers services on demand via the [Open Service Broker](https://github.com/openservicebrokerapi/servicebroker). The actual location of the External Service Provider is arbitrary.
  It can be a remote service provider like AWS S3, an on premise cluster, or colocated with the Service-Catalog.

  The External Service Provider should consist of two components: the service broker and the actual services being consumed by clients.  In this example, the service component is a CNS Object Storage cluster with a Swift/S3 REST API.

- *Broker*

  The broker presents a REST API which implements the [Open Service Broker](https://github.com/openservicebrokerapi/servicebroker) for http routes.
  It functions as the endpoint to which the Service-Catalog communicates all requests for service operations.

##### CNS Broker Objects

- *ServiceInstance*

  A Broker's internal representation of a provisioned service.

- *Credential*

  A Broker's data structure for tracking coordinates and auth credentials for a single *ServiceInstance*.  

- *Catalog*

  A complete list of services and plans that are provisionable by the Broker.

#### K8s Cluster

For this example, the local Kubernetes cluster is used to represent a development environment.
The Service-Catalog is expected to be run in the development cluster because, in addition to Service-Catalog API objects, it is responsible for creating core Kubernetes objects (e.g. Secrets), in the namespaces of service consumers.

##### Service-Catalog API Objects

Here is where naming collisions become confusing.
All of the terms in this section are objects of the Service Catalog API Server.
The terms here are only Kubernetes' representation of the actual services provisioned in the External Service Provider.
They are managed by the `SC-APISERVER` portion of the [control flow diagram](docs/diagram/control-diag.md).

**NOTE: There is no Catalog object in the Service Catalog.  It is represented as a set of ServiceClasses only!**

- *ServiceBroker*

  Service Catalog representation of the actual Broker, usually located elsewhere.
  The Service Catalog Controller Manager will use the URL provided within the *ServiceBroker* to connect to the actual Broker server.
  A *ServiceBroker* can offer many *ServiceClasses*.

- *ServiceClass*

  Service Catalog representation of a *Catalog* offering.  When a *ServiceBroker* is created, the Service Catalog Controller Manager requests the *Catalog* from the actual Broker.
  The response is a json object listing all Services and Plans offered by the Broker.
  The Controller Manager processes this response into a set of *ServiceClasses* in the API Server.
  In this case, our *ServiceClass* is a provisionable S3 bucket.
  A *ServiceClass* can provision many *ServiceInstances* of its class.

- *ServiceInstance*

  Service Catalog representation of a consumable service instance in the External Service Provider.
  In this case, a single S3 bucket.
  A *ServiceInstance* can have many *ServiceInstanceCredentials*, so long as the service supports this.
  This enables a single instance to be consumed by many Pods.

- *ServiceInstanceCredential*

  Service Catalog object that does not contain any authentication or coordinate information.
  Instead, when a *ServiceInstanceCredential* is created in the Service Catalog API Server, it triggers a request to the Broker for authentication and coordinate information.
  Once a response is received, the sensitive information is stored in a *Secret* in the same namespace as the *ServiceInstanceCredential*.

### What the CNS Object Broker Does
This broker implements [Open Service Broker](https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md) methods for creating, connecting to, and destroying
S3 buckets.
- For each new *ServiceInstance*, a new, uniquely named bucket is created.
- For each new *ServiceInstanceCredential*, a Secret is generated with the
coordinates and credentials of the *ServiceInstance's* associated bucket.
- Deleting a *ServiceInstance* destroys the associated bucket.
- Deleting a *ServiceInstanceCredential* deletes the secret.

### Limitations

- Currently, the CNS Object Broker is dependent on GCE.  If run outside of a GCE environment, it will fail to start because the CNS Object Broker detects the external IP of the node on which it is run by calling to the GCP metadata server.
It pairs this IP with the port of the `gluster-s3-deployment` *Service* to generate the coordinaates returned in the *ServiceInstanceCredential*.  This is not a requirement of brokers in general.

- As it is implemented, the CNS Object Broker must run on the same Kubernetes cluster as the `gluster-s3-deployment` because
it accesses the *Service* that exposes its *NodePort*.  This is done through a kubernetes api client, which will fail if it cannot
 reach the `gluster-s3-deployment`'s *Service*. This is not a requirement of brokers in general.

- Auth:  The S3 API implementation (Gluster-Swift) does not enforce any authentication / authorization.
Each new bucket, regardless of the *Namespace* of its *ServiceInstance*, is accessible and deletable by anyone with the coordinates of the S3 server.

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
- The following `gcloud config` variables are set:
  1. `account`
  2. `project`
  3. `zone`

## Setup

**NOTE: The following steps are performed from the local machine or VM unless stated otherwise.**

### Step 0: Preparing environment
- Clone [Kubernetes](https://github.com/kubernetes)
- Clone [Service-Catalog](https://github.com/kubernetes-incubator/service-catalog)
- Install [Google SDK](https://cloud.google.com/sdk/) and add `gcloud` to `PATH`
You must have an active Google Cloud Platform account.
- Configure `gcloud` account, zone, and project
These are detected by the deployment script and required for setup.

  `# gcloud config set account <user account>`

  `# gcloud config set project <project>`

  `# gcloud config set compute/zone <zone>`


### Step 1: Deploy Gluster-Kubernetes Cluster in Google Compute Engine (GCE)
This step sets up the **External Service Provider** portion of our topology (see [diagram](docs/diagram/control-diag.md)).

To kick off deployment, run `gk-cluster-deploy/gk-up.sh`
The script has a number of configurable variables relative to GCE Instance settings.
They can be found in `lib/config.sh`.
These can be overridden inline with `gk-up.sh` or as environment variables. To skip configuration review, run `gk-up.sh -y`.

Runtime takes around 5 to 10 minutes.

**NOTE: You can manually deploy the GCE portion by following [these instuctions](docs/manual-gce-deployment.md)**

When deployment completes, the URL of the CNS Object Broker will be output. *Note the URL and PORT.*

### Step 2: Deploy Kubernetes All-in-One Cluster
This step sets up the **K8s Cluster** portion of our topology (see [diagram](docs/diagram/control-diag.md)).
1. Change directories to the `kubernetes` repository.

2. Create the cluster

    `KUBE_ENABLE_CLUSTER_DNS=true ./hack/local-up-cluster.sh -O`

    This will stream to stdout/stderr in the terminal until it is killed with `ctrl-c`.

### Step 3: Deploy the Service-Catalog

*In a separate terminal:*
1. Change directories to the `./kubernetes-incubator/service-catalog/` repository.

2. Follow the [Service-Catalog Installation instructions](https://github.com/kubernetes-incubator/service-catalog/blob/master/docs/introduction.md#installation)
Once the Service-Catalog is deployed, return here.

## Using the Service Catalog

**STOP!** If you have made it this far, it is assumed that you now have

- A 4 node Kube cluster in GCE, running:
  - Gluster-Kubernetes and its S3 components.
  - The CNS Object Broker.

You can check the status of all these components at once by executing:

`# gcloud compute ssh <master node name> --command="kubectl get pod,svc --all-namespaces"`

### Create the *ServiceBroker* (API Object)

Change to the `cns-object-broker` directory created when cloning the repo.

0. Retrieve the URL and PORT of the cns-object-broker.

    If `gk-up.sh` was run, it will have been output at the end of the script

    To get the url and port manually, first note the **external ip** of any GCE node in the cluster.
    The broker is exposed via a *NodePort Service* and is reachable via any node.

    `# gcloud compute instances list --filter="<user name>"`

    Next, get the port exposed by the *NodePort Service*.

    `gcloud compute ssh <master node name> --command="kubectl get svc -n broker broker-cns-object-broker-node-port"`

    ```
    NAME                                 CLUSTER-IP      EXTERNAL-IP   PORT(S)          AGE
    broker-cns-object-broker-node-port   10.102.63.165   <nodes>       8080:32283/TCP   1d
    ```

    The ports are formatted as \<InternalPort\>:\<ExternalPort\>.
    Note the ExternalPort.

1.  Edit *examples/service-catalog/service-broker.yaml*

    Set the value of:
    ```yaml
    spec:
      url: http://<ExternalIP>:<ExternalPort>
    ```

2. Create the *ServiceBroker* api object.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-broker.yaml`

3. Verify the *ServiceBroker*.

    If successful, the Service-Catalog controller manager will have generated a *ServiceClass* for the `cns-bucket-service`.

    `# kubectl --context=service-catalog get servicebroker,serviceclasses`

    ```
    NAME                               AGE
    servicebrokers/cns-bucket-broker   28s

    NAME                                AGE
    serviceclasses/cns-bucket-service   28s
    ```

    If you do not see a *ServiceClass* object, see [Debugging](#debugging).

### Create the *ServiceInstance* (API Object)

1. *ServiceInstances* are namespaced.  Before proceeding, the *Namespace* must be created.

    `# kubectl create namespace test-ns`

    **NOTE: To set your own *Namespace*, edit examples/service-catalog/service-instance.yaml**

    Snippet:
    ```yaml
    kind: ServiceInstance
    metadata:
      namespace: test-ns  #Edit to set your own or use as is.
    ```

2. Now create the *ServiceInstance*.

    *Optional:* Set a custom bucket name.  If one is not provided, a random GUID is generated.
    Edit *examples/service-catalog/service-instance.yaml*.

    Snippet:
    ```yaml
    spec:
      parameters:
        bucketName: "cns-bucket-demo" #Optional
    ```

    Create the ServiceInstance.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-instance.yaml`

3. Verify the *ServiceInstance*.

    `# kubectl --context=service-catalog -n test-ns get serviceinstance cns-bucket-instance -o yaml`

    Look for the these values in the ouput:

    Snippet:
    ```yaml
    status:
      conditions:
        reason: ProvisionedSuccessfully
        message: The instance was provisioned successfully
    ```
    If the *ServiceInstance* fails to create, see [Debugging](#debugging).

### Create the *ServiceInstanceCredential* (API Object)

1. Create the *ServiceInstanceCredential*.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-instance-credential.yaml`

2. Verify the *ServiceInstanceCredential*.

    `# kubectl --context=service-catalog -n test-ns get serviceinstancecredentials`

    *ServiceInstanceCredentials* will result in a *Secret* being created in same Namespace.
    Check for the secret:

    `# kubectl -n test-ns get secret cns-bucket-credentials`

    ```
    NAME                     TYPE                                  DATA      AGE
    cns-bucket-credentials   Opaque                                4         2m
    ```

    If you want to verify the data was transmitted correctly, get the secret's yaml spec.

    `# kubectl -n test-ns get secret cns-bucket-credentials -o yaml`

    Snippet:
    ```yaml
    apiVersion: v1
    kind: Secret
    data:
      bucketEndpoint: MTA0LjE5Ny40LjIzOTozMDI5OA==
      bucketID: amNvcGU6amNvcGU=
      bucketName: Y25zLWJ1Y2tldC1kZW1v
      bucketPword: amNvcGU=
    ```

    Decode the data:

    `# echo "<value>"" | base64 -d`

## Debugging

#### Broker Log

To determine if the broker has encountered an error that may be impacting *ServiceInstance* creation,
it can be useful to examine the broker's log.

1. Access the Broker log by first ssh-ing into the GCE cluster.

    `# gcloud compute ssh <master node name>`

2. Get the unique name of the Broker *Pod*.

    `# kubectl get pods -n broker`

3. Using the Broker *Pod's* name, use `kubectl` to output the logs.

    `# kubectl -n broker logs -f <broker pod name>`

####  Inspecting Service-Catalog API Objects

Service-Catalog objects can return yaml or json formatted data just like core Kubernetes api objects.
To output the data, the command is:

`# kubectl --context=service-catalog get -o yaml <service-catalog object>`

Where `<service-catalog object>` is:
- `servicebroker`
- `serviceclass`
- `serviceinstance`
- `serviceinstancecredential`

#### Redeploy Service Catalog

Sometimes it's just quicker to tear it down and start again.  Thanks to Helm, this is relatively painless to do.

1. Tear down Service-Catalog

    `# helm delete --purge catalog`

2. Deploy Service-Catalog

    `# helm install charts/catalog --name catalog --namespace catalog`

Once Service-Catalog has returned to a Running/Ready status, you can begin again by [creating a ServiceBroker object](#create-the-servicebroker-api-object).
