# Ceph RGW Object Broker

## WARNING!
The work in this project is a proof of concept and not intended for production.

## Utilize Kubernetes Service-Catalog to dynamically provision CNS Object Storage.

## Overview
The Ceph RGW object broker is heavily based on the [CNS Object Broker](https://github.com/yard-turkey/cns-object-broker). This includes
the following documentation.
A core feature of the Kubernetes system is the ability to provision block and file storage on demand.
This project provides the ability to use the [Service-Catalog](https://github.com/kubernetes-incubator/service-catalog) and the RGW Object Broker
to provision Ceph RGW backed S3 buckets on demand.
A Ceph RGW installation can the S3 interface and backing storage. Service-Catalog enables communication between a client Kubernetes cluster and service provider. The RGW Object Broker is the endpoint to which the Service-Catalog sends requests for services.

The RGW Object Broker handles requests to create and destroy RGW users and buckets and returns information and credentials that are required to use them.


### Topology
A Ceph cluster that has a configured RGW service is required. The RGW endpoint will need to be accessible by the RGW object broker. The Ceph cluster
can be installed anywhere, not necesarily on Kubernetes.
The RGW object broker needs to be installed on a Kubernetes cluster. The Service-Catalog can be installed on a separate
Kubernetes cluster.

### Nomenclature

Before going further, it's useful to define common terms used throughout the entire system.
The relationships between the different systems and objects can be seen in the [control flow diagram](docs/diagram/control-diag.md).
There are a number of naming collisions which can lead to some confusion.

#### External Service Provider

- *External Service Provider*

  For our purposes, an External Service Provider is any system that offers services on demand via the [Open Service Broker](https://github.com/openservicebrokerapi/servicebroker). The actual location of the External Service Provider is arbitrary.
  It can be a remote service provider like AWS S3, an on premise cluster, or colocated with the Service-Catalog.

  The External Service Provider should consist of two components: the service broker and the actual services being consumed by clients.  In this example, the service component is a RGW Object Storage cluster with a S3 REST API.

- *Broker*

  The broker presents a REST API which implements the [Open Service Broker](https://github.com/openservicebrokerapi/servicebroker) for http routes.
  It functions as the endpoint to which the Service-Catalog communicates all requests for service operations.

##### RGW Broker Objects

- *ServiceInstance*

  A Broker's internal representation of a provisioned service.

- *Bind*

  A Broker's data structure for tracking coordinates and auth credentials for a single *ServiceInstance*. Each bind has a set of credentials
  allowing access of data by the service instance user.

- *Catalog*

  A complete list of services and plans that are provisionable by the Broker.

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
  In this case, our *ServiceClass* is a provisionable RGW S3 bucket.
  A *ServiceClass* can provision many *ServiceInstances* of its class.

- *ServiceInstance*

  Service Catalog representation of a consumable service instance in the External Service Provider.
  In this case, a single bucket.
  A *ServiceInstance* can have many *Binds*, so long as the service supports this.
  This enables a single instance to be consumed by many Pods.

- *Bind*

  Service Catalog object that does not contain any authentication or coordinate information.
  Instead, when a *Bind* is created in the Service Catalog API Server, it triggers a request to the Broker for authentication and coordinate information.
  Once a response is received, the sensitive information is stored in a *Secret* in the same namespace as the *Bind*.

### What the RGW Object Broker Does
This broker implements [Open Service Broker](https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md) methods for creating, connecting to, and destroying
S3 buckets.
- For each new *ServiceInstance*, a new, uniquely named user is created, for which a new bucket is created.
- For each new *Bind*, a new S3 access-key and secret is are generated for the user that is associated with the service instance. The
bucket name, the RGW endpoint and the credentials are provided.
- Deleting a *Bind* removes the access-key/secret pair.
- Deleting a *ServiceInstance* suspends the generated user and bucket, and relinks the bucket to a different garbage collection user, where it can later be destroyed.

---

## Installation

### Dependencies
- [Kubernetes](https://github.com/kubernetes/kubernetes): to run local K8s cluster
- Kubernetes-Incubator [Service-Catalog](https://github.com/kubernetes-incubator/service-catalog): to provide Service-Catalog helm chart
- Kubernetes [Helm](https://github.com/kubernetes/helm#install): to install Service-Catalog chart

### Assumptions
- Ceph cluster with RGW service is deployed

## Setup

**NOTE: The following steps are performed from the local machine or VM unless stated otherwise.**

### Step 1: Preparing environment
- Clone [Kubernetes](https://github.com/kubernetes)
- Clone [Service-Catalog](https://github.com/kubernetes-incubator/service-catalog)


### Step 2: Deploy Kubernetes

For example the [Rook](https://rook.io/docs/rook/master/development-environment.html) instructions has instructions to install Kuberentes.

### Step 3: Deploy the Service-Catalog

*In a separate terminal:*
1. Change directories to the `./kubernetes-incubator/service-catalog/` repository.

2. Follow the [Service-Catalog Installation instructions](https://github.com/kubernetes-incubator/service-catalog/blob/master/docs/install.md)
Once the Service-Catalog is deployed, return here.

## Installing the Service Broker
**STOP!** If you have made it this far, it is assumed that you now have

- A Ceph cluster with RGW service configured
- A Kubernetes cluster installed

3. Create RGW user that will be used as the admin user

    `$ radosgw-admin user create --uid=kube-broker --caps="metadata=read,write;users=read,write,buckets=read,write"`

    Note the generated access_key and secret_key pairs that were generated for that user

3. Install the RGW service broker

Update charts/values.yaml.template with the RGW endpoint, and the admin user's credentials.

    ```
    $ make broker
    $ scripts/install-broker.sh
    ```

---

## Using the Service Catalog

**STOP!** If you have made it this far, it is assumed that you now have

- A Ceph cluster with RGW service configured
- The RGW Object Broker running on a Kubernetes cluster

You can check the status of all the Kubernetes related components

    $ kubectl get pod,svc --all-namespaces

### Create the *ServiceBroker* (API Object)

Change to the `rgw-object-broker` directory created when cloning the repo.

0. Retrieve the IP address and port of the rgw-object-broker.

    First identify the the **external ip** address of the Kubernetes cluster where the RGW Service broker
    is running.

    Next, get the port exposed by the *NodePort Service*.

    `kubectl get svc -n broker broker-rgw-object-broker-node-port`

    ```
    NAME                                 CLUSTER-IP      EXTERNAL-IP   PORT(S)          AGE
    broker-rgw-object-broker-node-port   10.102.63.165   <nodes>       8080:32283/TCP   1d
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

    If successful, the Service-Catalog controller manager will have generated a *ServiceClass* for the `rgw-bucket-service`.

    `# kubectl --context=service-catalog get servicebroker,serviceclasses`

    ```
    NAME                               AGE
    servicebrokers/rgw-bucket-broker   28s

    NAME                                AGE
    serviceclasses/rgw-bucket-service   28s
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
        bucketName: "rgw-bucket-demo" #Optional
    ```

    Create the ServiceInstance.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-instance.yaml`

3. Verify the *ServiceInstance*.

    `# kubectl --context=service-catalog -n test-ns get serviceinstance rgw-bucket-instance -o yaml`

    Look for the these values in the ouput:

    Snippet:
    ```yaml
    status:
      conditions:
        reason: ProvisionedSuccessfully
        message: The instance was provisioned successfully
    ```
    If the *ServiceInstance* fails to create, see [Debugging](#debugging).

### Create the *Bind* (API Object)

1. Create the *Bind*.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-instance-credential.yaml`

2. Verify the *Bind*.

    `# kubectl --context=service-catalog -n test-ns get servicebindings`

    *ServiceInstanceCredentials* will result in a *Secret* being created in same Namespace.
    Check for the secret:

    `# kubectl -n test-ns get secret rgw-bucket-credentials`

    ```
    NAME                     TYPE                                  DATA      AGE
    rgw-bucket-credentials   Opaque                                4         2m
    ```

    If you want to verify the data was transmitted correctly, get the secret's yaml spec.

    `# kubectl -n test-ns get secret rgw-bucket-credentials -o yaml`

    Snippet:
    ```yaml
    apiVersion: v1
    kind: Secret
    data:
      accessKey: VVBLRTdNMlFWNjVBVU01UlFNRDE=
      bucketEndpoint: aHR0cDovLzEwLjE3LjExMi4yOjgwMDA=
      bucketName: YmM3bG1lMWx1dHFnMDBmZ2dmcWc=
      secretKey: cEE0Z1VZSDJIeVlmN3J0OWZtUGJmeDNYNVJUQU9BQXNNOGxxM1ozdQ==
      userName: bXlrdWJlLWJjN2xtZTFsdXRxZzAwZmdnZnIw
    ```

    Decode the data:

    `# echo "<value>"" | base64 -d`

---

## Debugging

#### Broker Log

To determine if the broker has encountered an error that may be impacting *ServiceInstance* creation,
it can be useful to examine the broker's log.

1. Get the unique name of the Broker *Pod*.

    `# kubectl get pods -n broker`

2. Using the Broker *Pod's* name, use `kubectl` to output the logs.

    `# kubectl -n broker logs -f <broker pod name>`

####  Inspecting Service-Catalog API Objects

Service-Catalog objects can return yaml or json formatted data just like core Kubernetes api objects.
To output the data, the command is:

`# kubectl --context=service-catalog get -o yaml <service-catalog object>`

Where `<service-catalog object>` is:
- `servicebroker`
- `serviceclass`
- `serviceinstance`
- `bind`

#### Redeploy Service Catalog

Sometimes it's just quicker to tear it down and start again.  Thanks to Helm, this is relatively painless to do.

1. Tear down Service-Catalog

    `# helm delete --purge catalog`

2. Deploy Service-Catalog

    `# helm install charts/catalog --name catalog --namespace catalog`

Once Service-Catalog has returned to a Running/Ready status, you can begin again by [creating a ServiceBroker object](#create-the-servicebroker-api-object).
