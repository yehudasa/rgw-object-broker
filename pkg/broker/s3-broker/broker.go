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

package s3_broker

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"github.com/golang/glog"

	"github.com/kubernetes-incubator/service-catalog/pkg/brokerapi"
	"github.com/minio/minio-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	k8sRest "k8s.io/client-go/rest"
)

type s3ServiceInstance struct {
	// k8s namespace
	Namespace string
	// binding credential created during Bind()
	Credential brokerapi.Credential // s3 server url, includes port and bucket name
}

type s3Broker struct {
	// rwMutex controls concurrent R and RW access
	rwMutex sync.RWMutex
	// instanceMap maps instanceIDs to the ID's userProvidedServiceInstance values
	instanceMap map[string]*s3ServiceInstance
	// client used to access s3 API
	s3Client *minio.Client
	// s3 server ip and port
	s3url string // includes ":port"
	s3ID  string
	s3Pwd string
	// client used to access kubernetes
	kubeClient *clientset.Clientset
}

// CreateBroker initializes the service broker. This function is called by server.Start()
func CreateBroker() Broker {
	const S3_BROKER_POD_LABEL = "glusterfs=s3-pod"
	var instanceMap = make(map[string]*s3ServiceInstance)
	glog.Info("Generating new broker.")
	s3ip, err := getExternalIP()
	if err != nil {
		glog.Errorf("Failed to get external IP: %v", err)
	}

	// get the kubernetes client
	cs, err := getKubeClient()
	if err != nil {
		glog.Fatalf("failed to get kubernetes client: %v\n", err)
	}

	// get the s3 deployment pod created via the `gk-deploy` script
	// need to get this pod using label selectors since its name is generated
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
	}
	s3Port := svc.Spec.Ports[0].NodePort // int

	// get the s3 client
	s3endpoint := fmt.Sprintf("%s:%d", s3ip, s3Port)
	s3c, err := getS3Client(acct, user, pass, s3endpoint)
	if err != nil {
		glog.Fatalf("failed to get minio-s3 client: %v\n", err)
	}
	glog.Infof("New Broker for s3 endpoint: %s", s3endpoint)
	return &s3Broker{
		instanceMap: instanceMap,
		s3Client:    s3c,
		s3url:       s3endpoint,
		kubeClient:  cs,
		s3ID:        fmt.Sprintf("%s:%s", acct, user),
		s3Pwd:       pass,
	}
}

func (b *s3Broker) Catalog() (*brokerapi.Catalog, error) {
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

func (b *s3Broker) GetServiceInstanceLastOperation(instanceID, serviceID, planID, operation string) (*brokerapi.LastOperationResponse, error) {
	glog.Info("GetServiceInstanceLastOperation not yet implemented.")
	return nil, nil
}

func (b *s3Broker) CreateServiceInstance(instanceID string, req *brokerapi.CreateServiceInstanceRequest) (*brokerapi.CreateServiceInstanceResponse, error) {
	glog.Info("CreateServiceInstance called.  instanceID: %s", instanceID)
	b.rwMutex.Lock()
	defer b.rwMutex.Unlock()
	const (
		BUCKET_ENDPOINT = "bucketEndpoint"
		BUCKET_NAME     = "bucketName"
		BUCKET_ID       = "bucketID"
		BUCKET_PWORD    = "bucketPword"
	)
	// does service instance exist?
	if _, ok := b.instanceMap[instanceID]; ok {
		return nil, fmt.Errorf("ServiceInstance %q already exists", instanceID)
	}
	// Check required parameter "bucketName"
	bucketName, ok := req.Parameters["bucketName"].(string)
	if ! ok {
		return nil, fmt.Errorf("Paramters[\"bucketName\"] not provided.  Please define a bucket name.")
	}
	glog.Info("Creating new bucket: %q for instance %q.", bucketName, instanceID)
	// create new service instance
	err := b.provisionBucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("Failed to provision bucket <%s>: %v", bucketName, err)
	}
	b.instanceMap[instanceID] = &s3ServiceInstance{
		Namespace: req.ContextProfile.Namespace,
		Credential: brokerapi.Credential{
			BUCKET_NAME:     bucketName,
			BUCKET_ENDPOINT: b.s3url,
			BUCKET_ID:       b.s3ID,
			BUCKET_PWORD:    b.s3Pwd,
		},
	}
	return nil, nil
}

// TODO
func (b *s3Broker) RemoveServiceInstance(instanceID, serviceID, planID string, acceptsIncomplete bool) (*brokerapi.DeleteServiceInstanceResponse, error) {
	glog.Info("RemoveServiceInstance not yet implemented.")
	return nil, nil
}

func (b *s3Broker) Bind(instanceID, bindingID string, req *brokerapi.BindingRequest) (*brokerapi.CreateServiceBindingResponse, error) {
	instance, ok := b.instanceMap[instanceID]
	if ! ok {
		return nil, fmt.Errorf("Instance ID %q not found.", instanceID)
	}
	creds := instance.Credential
	if creds == nil {
		return nil, fmt.Errorf("No credentials found for instance %q.", instanceID)
	}
	return &brokerapi.CreateServiceBindingResponse{
		Credentials: creds,
	}, nil
}

// nothing to do here
func (b *s3Broker) UnBind(instanceID, bindingID, serviceID, planID string) error {
	glog.Info("UnBind not yet implemented.")
	return nil
}

func (b *s3Broker) provisionBucket(bucket string) error {
	glog.Infof("provisionBucket(name: %q).", bucket)
	location := "" // ignored for now...
	// check if bucket already exists
	exists, err := b.s3Client.BucketExists(bucket)
	if err == nil && exists {
		return fmt.Errorf("Bucket %q already exists.  S3-Client: %v", bucket, err)
	}
	// create new bucket
	glog.Infof("Creating bucket %q.", bucket)
	err = b.s3Client.MakeBucket(bucket, location)
	if err != nil {
		return fmt.Errorf("S3-Client.MakeBucket err: %v", err)
	}
	glog.Infof("Bucket %q created.", bucket)
	return nil
}

// getS3Client returns a minio api client
func getS3Client(acct, user, pass, ip string) (*minio.Client, error) {
	glog.Infof("Creating s3 client based on: \"%s:%s\" on ip %s", acct, user, ip)

	id := fmt.Sprintf("%s:%s", acct, user)
	useSSL := false
	minioClient, err := minio.NewV2(ip, id, pass, useSSL)
	if err != nil {
		return nil, fmt.Errorf("Unable to create minio S3 client: %v", err)
	}
	return minioClient, nil
}

// getKubeClient returns a k8s api client
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
		glog.Errorf("Failed to sent http request: %v", err)
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
