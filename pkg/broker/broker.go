/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package broker

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/rs/xid"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/kubernetes-incubator/service-catalog/pkg/brokerapi"
	"github.com/minio/minio-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	k8sRest "k8s.io/client-go/rest"
)

const (
	BUCKET_ENDPOINT = "bucketEndpoint"
	BUCKET_NAME     = "bucketName"
	BUCKET_ID       = "bucketID"
	BUCKET_PASS	= "bucketPass"
)

type s3ServiceInstance struct {
	// k8s namespace
	Namespace string
	// binding credential created during Bind()
	Credential brokerapi.Credential // s3 server url, includes port and bucket name
}

type broker struct {
	// rwMutex controls concurrent R and RW access
	rwMutex	    sync.RWMutex
	// instanceMap maps instanceIDs to the ID's userProvidedServiceInstance values
	instanceMap map[string]*s3ServiceInstance
	// client used to access s3 API
	s3Client    *minio.Client
	// s3 server ip and port
	s3url	    string // includes ":port"
	s3ID	    string
	s3Pass	    string
	// client used to access kubernetes
	kubeClient  *clientset.Clientset
}

// Initialize the s3-gluster service broker. This function is called by `server.Start()`.
func CreateBroker() Broker {
	var instanceMap = make(map[string]*s3ServiceInstance)
	glog.Info("Generating new s3 broker.")

	// get the kubernetes client
	cs, err := getKubeClient()
	if err != nil {
		glog.Fatalf("failed to get kubernetes client: %v\n", err)
	}

        /*

	// Get the s3 deployment pod created via the `gk-deploy` script. Need to
	// get this pod using label selectors since its name is generated.
	ns := "default"
	podList, err := cs.CoreV1().Pods(ns).List(metav1.ListOptions{
		LabelSelector: S3_BROKER_POD_LABEL,
	})
	if err != nil || len(podList.Items) != 1 {
		glog.Fatalf("failed to get s3-deploy pod via label %q: %v\n", S3_BROKER_POD_LABEL, err)
	}
	s3Pod := podList.Items[0]

	// get user, account and password from s3 pod (supplied to the `gk-deloy` script)
	acct, user, pass := "", "", ""
	for _, pair := range s3Pod.Spec.Containers[0].Env {
		switch pair.Name {
		case "S3_ACCOUNT":
			acct = pair.Value
		case "S3_USER":
			user = pair.Value
		case "S3_PASSWORD":
			pass = pair.Value
		default:
			glog.Fatalf("unexpected env key %q for s3-deploy pod %q\n", pair.Name, s3Pod.Name)
		}
	}

	// get s3 deployment service in order to get the s3 broker's external port
	svcName := "gluster-s3-deployment"
	svc, err := cs.Services(ns).Get(svcName, metav1.GetOptions{})
	if err != nil {
		glog.Fatalf("failed to get s3 service %q: %v\n", svcName, err)
	} */
        s3ip := "10.17.112.2"
	s3Port := 8000
        user := "AR04WP16QTGI9C5IQ4BX"
        pass := "0ZPTti4DLES2NDJ6qkemWZsHcasmA2xkNfyqrKgN"

	// get the s3 client
	s3endpoint := fmt.Sprintf("%s:%d", s3ip, s3Port)
	s3c, err := getS3Client(user, pass, s3endpoint)
	if err != nil {
		glog.Fatalf("failed to get minio-s3 client: %v\n", err)
	}

	glog.Infof("New Broker for s3 endpoint: %s", s3endpoint)
	return &broker{
		instanceMap: instanceMap,
		s3Client:    s3c,
		s3url:       s3endpoint,
		kubeClient:  cs,
		s3ID:        user,
		s3Pass:      pass,
	}
}
// Implements the `Catalog` interface method.
func (b *broker) Catalog() (*brokerapi.Catalog, error) {
	return &brokerapi.Catalog{
		Services: []*brokerapi.Service{
			{
				Name:        "cns-bucket-service",
				ID:          "0",
				Description: "A bucket of storage object backed by CNS.",
				Bindable:    true,
				Plans: []brokerapi.ServicePlan{
					{
						Name:        "default",
						ID:          "0",
						Description: "The best plan, and the only one.",
						Free:        true,
					},
				},
			},
		},
	}, nil
}

// The `GetServiceInstanceLastOperation` interface method is not implemented.
func (b *broker) GetServiceInstanceLastOperation(instanceID, serviceID, planID, operation string) (*brokerapi.LastOperationResponse, error) {
	glog.Info("GetServiceInstanceLastOperation not yet implemented.")
	return nil, nil
}

// Implements the `CreateServiceInstance` interface method by creating (provisioning) a s3 bucket.
// Note: (nil, nil) is returned for success, meaning the CreateServiceInstanceResponse is ignored by
//   the caller.
func (b *broker) CreateServiceInstance(instanceID string, req *brokerapi.CreateServiceInstanceRequest) (*brokerapi.CreateServiceInstanceResponse, error) {
	glog.Infof("CreateServiceInstance called.  instanceID: %s", instanceID)
	b.rwMutex.Lock()
	defer b.rwMutex.Unlock()
	// does service instance exist?
	if _, ok := b.instanceMap[instanceID]; ok {
		glog.Errorf("Instance requested already exists.")
		return nil, fmt.Errorf("ServiceInstance %q already exists", instanceID)
	}
	// Check required parameter "bucketName"
	bucketName, ok := req.Parameters["bucketName"].(string)
	if !ok {
		glog.Errorf("Bucket name not provided, generating random name.")
		bucketName = xid.New().String()
	}
	glog.Infof("Creating new bucket: %q for instance %q.", bucketName, instanceID)
	// create new service instance
	exists, err := b.checkBucketExists(bucketName)
	if err != nil {
		return nil, fmt.Errorf("Error occured while checking bucket name availability.")
	}
	if exists {
		glog.Errorf("Bucket name unavailable: %q already exists.", bucketName)
		return nil, fmt.Errorf("Bucket name unavailable: %q already exists.", bucketName)
	}
	if err := b.provisionBucket(bucketName); err != nil {
		return nil, err
	}
	b.instanceMap[instanceID] = &s3ServiceInstance{
		Namespace: req.ContextProfile.Namespace,
		Credential: brokerapi.Credential{
			BUCKET_NAME:     bucketName,
			BUCKET_ENDPOINT: b.s3url,
			BUCKET_ID:       b.s3ID,
			BUCKET_PASS:	 b.s3Pass,
		},
	}
	return nil, nil
}

// Implements the `RemoveServiceInstance` interface method.
func (b *broker) RemoveServiceInstance(instanceID, serviceID, planID string, acceptsIncomplete bool) (*brokerapi.DeleteServiceInstanceResponse, error) {
	glog.Infof("RemoveServiceInstance called. instanceID: %s", instanceID)
	b.rwMutex.Lock()
	defer b.rwMutex.Unlock()
	instance, ok := b.instanceMap[instanceID]
	if !ok {
		glog.Errorf("InstanceID %q not found.", instanceID)
		return nil, fmt.Errorf("Broker cannot find instanceID %q.", instanceID)
	}
	bucketName := instance.Credential[BUCKET_NAME].(string)
	exists, err := b.checkBucketExists(bucketName)
	if err != nil {
		return nil, fmt.Errorf("Error checking if bucket exists: %v", err)
	}
	if exists {
		glog.Infof("Removing bucket %q", bucketName)
		if err := b.s3Client.RemoveBucket(bucketName); err != nil {
			glog.Errorf("Error during RemoveBucket: %v", err)
			return nil, fmt.Errorf("S3 Client errored while removing bucket: %v", err)
		}
	}
	delete(b.instanceMap, BUCKET_NAME)
	glog.Infof("Remove bucket %q succeeded.", bucketName)
	return nil, nil
}

// Implements the `Bind` interface method.
func (b *broker) Bind(instanceID, bindingID string, req *brokerapi.BindingRequest) (*brokerapi.CreateServiceBindingResponse, error) {
	glog.Infof("Bind called. instanceID: %q", instanceID)
	instance, ok := b.instanceMap[instanceID]
	if !ok {
		glog.Errorf("Instance ID %q not found.")
		return nil, fmt.Errorf("Instance ID %q not found.", instanceID)
	}
	if len(instance.Credential) == 0 {
		glog.Errorf("Instance %q is missing credentials.", instanceID)
		return nil, fmt.Errorf("No credentials found for instance %q.", instanceID)
	}
	glog.Infof("Bind instance %q succeeded.", instanceID)
	return &brokerapi.CreateServiceBindingResponse{
		Credentials: instance.Credential,
	}, nil
}

// nothing to do here
// The `UnBind` interface method is not implemented.
func (b *broker) UnBind(instanceID, bindingID, serviceID, planID string) error {
	glog.Info("UnBind not yet implemented.")
	return nil
}

// Creates an s3 compatible bucket of the passed-in name.
func (b *broker) provisionBucket(bucketName string) error {
	glog.Infof("Creating bucket %q", bucketName)
	location := "" // ignored for now...
	// create new bucket
	err := b.s3Client.MakeBucket(bucketName, location)
	if err != nil {
		glog.Errorf("Error creating bucket: %v", err)
		return fmt.Errorf("S3-Client.MakeBucket err: %v", err)
	}
	glog.Infof("Create bucket %q succeeded.", bucketName)
	return nil
}

// Returns true if the passed-in bucket exists.
// TODO: long way of checking for bucket name collision. gluster-swift does not support
//   the api call that minio.BucketExists() maps to; always fails with "400 bad request".
func (b *broker) checkBucketExists(bucketName string) (bool, error) {
	glog.Infof("Checking if bucket name %q already exists.", bucketName)
	buckets, err := b.s3Client.ListBuckets()
	if err != nil {
		glog.Errorf("Error occurred during ListBuckets: %v", err)
		return false, fmt.Errorf("Error occurred during ListBuckets: %v", err)
	}
	exists := false
	for _, bucket := range buckets {
		if bucketName == bucket.Name {
			exists = true
			break
		}
	}
	return exists, nil
}

// Returns a minio api client.
func getS3Client(user, pass, ip string) (*minio.Client, error) {
	glog.Infof("Creating s3 client based on: \"%s\" on ip %s", user, ip)

	useSSL := false
	minioClient, err := minio.NewV2(ip, user, pass, useSSL)
	if err != nil {
		return nil, fmt.Errorf("Unable to create minio S3 client: %v", err)
	}
	return minioClient, nil
}

// Returns a k8s api client.
func getKubeClient() (*clientset.Clientset, error) {
	glog.Info("Getting k8s API Client config")
	kubeClientConfig, err := k8sRest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to create k8s in-cluster config: %v", err)
	}
	glog.Info("Creating new Kubernetes Clientset")
	cs, err := clientset.NewForConfig(kubeClientConfig)
	return cs, err
}

// Returns the external ip of the gce master node.
func getExternalIP() (string, error) {
	c := http.Client{}
	glog.Info("Requesting external IP.")
	req, err := http.NewRequest("GET", "http://metadata/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip", nil)
	if err != nil {
		glog.Errorf("Failed to create new http request: %v", err)
		return "", err
	}
	req.Header.Add("Metadata-Flavor", " Google")
	resp, err := c.Do(req)
	if err != nil {
		glog.Errorf("Failed to send http request: %v", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorf("Failed to decode http response body: %v", err)
		return "", err
	}
	glog.Infof("Got external ip: %v", string(body))
	return string(body), nil
}
