# CNS Object Broker
## Utilize Kubernetes Service-Catalog to dynamically provision CNS Object Storage.

## WARNING!
The work in this project is a proof of concept and not intended production.

## Overview
A core feature of the Kubernetes system is the ability to provision a diverse
offering of block and file storage on demand.  This project seeks to demonstrate
that by using the Kubernetes-Incubator's Service-Catalog, it is now also possible
to bring this dynamic provisioning to S3 object storage.  This broker is designed
to be used with [Gluster-Kubernetes](https://github.com/jarrpa/gluster-kubernetes).

See our detailed [command flow diagram](./docs/Service-Catalog-Published.png) for
 an overview of the system.

### What It Does
This broker implements OpenServiceBroker methods for creating, connecting to, and destroying
object buckets.
- For each new Service Instance, a new, unquiely named bucket is created.
- For each new Service Instance Credential, a Secret is generated with the
coordinates and credentials of the Service Instance's associated bucket.
- Deleting a Service Instance destroys the associated bucket.
- Deleting a Service Instance Credential deletes the secret.  Nothing is done on the broker side.  This is because the broker does not perform any actions on Bind, so there is nothing to
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
and its colocated CNS Object store.  They fill the role of our micro-service provider.
A second system will be the locally running Kubernetes cluster on which with Service-Catalog is deployed.  This cluster will be our micro-server client.  Please refer to the [command flow diagram](./docs/control-diag.md) for a more in depth look at these systems.

## Setup
### Step 0: Preparing environment
- Clone [Kubernetes](https://github.com/kubernetes)
- Clone [Service-Catalog](https://github.com/kubernetes-incubator/service-catalog)
- Install [Google SDK](https://cloud.google.com/sdk/) and add `gcloud` to `PATH`.  You must have an active Google Cloud Platform account.
- Configure `gcloud` user, zone, and project.  These are detected by the deployment script and required for setup.


`# gcloud config set account <user account>`

`# gcloud config set project <project>`

`# gcloud config set compute/zone <zone>`


### Step 1: Deploy Gluster-Kubernetes cluster in Google Compute Engine (GCE)
This step sets up the **External Service Provider** portion of our topology (see [diagram](./docs/Service-Catalog-Published.png)).

To kick off deployment, run `gk-cluster-deploy/deploy/cluster/gk-up.sh`.  The script has a number of configurable
variables relative to GCE Instance settings.  These can be defined inline with `gk-up.sh` or as environment variables.  To run the script as fire and forget, pass `-y` to skip configuration review.

Runtime takes around 5 to 10 minutes.  Go get some coffee.

Alternatively, you can manually deploy the GCE portion by following [these instuctions](//TODO).  // TODO

When deployment completes, commands to ssh into the master node and the URL of the CNS Object Broker will be output. **Note the URL and PORT.**

### Step 2: Deploy the Service-Catalog
This step sets up the **K8s Cluster** portion of our topology (see [diagram](./docs/Service-Catalog-Published.png)).

*In a separate terminal:*
1. Change directories to the `service-catalog` repository.

2. Follow the [Service-Catalog Installation instructions](https://github.com/kubernetes-incubator/service-catalog/blob/master/docs/introduction.md#installation).  Once the Service-Catalog is deployed, return here.


## Using the Service Catalog

**STOP!** If you have made it this far, it is assumed that you now have

- A 4 node Kube cluster in GCE, running:
  - Gluster-Kubernetes and its S3 components.
  - Our CNS Object Broker.

You can check the status of all these components at once by executing

`# gcloud compute ssh <master node name> --command="kubectl get pod,svc --all-namespaces"`

### Create the *ServiceBroker* (API Object)

Change to the `cns-object-broker` directory created when cloning the repo.

0. Retrieve the URL and PORT of the cns-object-broker.

    If `gk-up.sh` was run, it will have been output at the end of the script.  

    To get the url and port manually, first note the **external ip** of any GCE node in the cluster.  The broker is exposed via a *NodePort Service* and so is reachable via any node.

    `# gcloud compute instances list --filter="<user name>"`

    Next, get the port exposed by the *NodePort Service*.

    `gcloud compute ssh <master node name> --command="kubectl get svc -n broker broker-cns-object-broker-node-port"`

    ```
    NAME                                 CLUSTER-IP      EXTERNAL-IP   PORT(S)          AGE
    broker-cns-object-broker-node-port   10.102.63.165   <nodes>       8080:32283/TCP   1d
    ```

    The ports are formatted as \<InternalPort\>:\<ExternalPort\>.  Note the ExternalPort.

1.  Edit *examples/service-catalog/service-broker.yaml*

    Set the value of:
    ```yaml
    spec:
      url: http://<ExternalIP>:<ExternalPort>
    ```

2. Create the *ServiceBroker* api object.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-broker.yaml`

3. Verify the *ServiceBroker*.

    If successful, the service-catalog controller will have generated a *ServiceClass* for the `cns-bucket-service`

    `# kubectl --context=service-catalog get servicebroker,serviceclasses`

    ```
    NAME                               AGE
    servicebrokers/cns-bucket-broker   28s

    NAME                                AGE
    serviceclasses/cns-bucket-service   28s
    ```

    If you do not see a *ServiceClass* object, see [Troubleshooting](#Troubleshooting). //TODO

### Create the *ServiceInstance* (API Object)

1. *ServiceInstances* are Namespaced.  Before proceeding, the *Namespace* must be created.

    `# kubectl create namespace test-ns`

    *Note:* To change the *Namespace* name, edit *examples/service-catalog/service-instance.yaml*

    Snippet:
    ```yaml
    kind: ServiceInstance
    metadata:
      namespace: test-ns  # Edit
    ```

2. Now create the *ServiceInstance*.

    *Optional:* Set a custom bucket name.  If one is not provided, a random GUID is generated.  Edit *examples/service-catalog/service-instance.yaml*

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
    If the *ServiceInstance* fails to create, see [troubleshooting](//TODO). //TODO

### Create the *ServiceInstanceCredential* (API Object)

1. Create the *ServiceInstanceCredential*.

    `# kubectl --context=service-catalog create -f examples/service-catalog/service-instance-credential.yaml`

2. Verify the *ServiceInstanceCredential*.

    `# kubectl --context=service-catalog -n test-ns get serviceinstancecredentials`

    *ServiceInstanceCredentials* will result in a *Secret* being created in same Namespace.  Check for the secret:

    `# kubectl -n test-ns get secret cns-bucket-credentials`

    ```
    NAME                     TYPE                                  DATA      AGE
    cns-bucket-credentials   Opaque                                4         2m
    ```

    If you want to verify the data was transmitted correctly, get the secret's yaml spec.

    `#  kubectl -n test-ns get secret cns-bucket-credentials -o yaml`

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

## Troubleshooting

// TODO
