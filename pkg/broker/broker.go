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
	"os"
	"strings"
	"time"
	"bytes"
	"io/ioutil"
	"encoding/json"
	"github.com/golang/glog"
	"github.com/rs/xid"
	"net/http"
	"net/url"
	"sync"

	"github.com/kubernetes-incubator/service-catalog/pkg/brokerapi"
	"github.com/minio/minio-go"
	"github.com/minio/minio-go/pkg/s3signer"
        // metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	k8sRest "k8s.io/client-go/rest"
)

const (
	USER_NAME       = "userName"
	BUCKET_ENDPOINT = "bucketEndpoint"
	BUCKET_NAME     = "bucketName"
	ACCESS_KEY      = "accessKey"
	SECRET_KEY	= "secretKey"
)

type s3ServiceInstance struct {
	// k8s namespace
	Namespace string
	// binding credential created during Bind()
	Credential brokerapi.Credential // s3 server url, includes port and bucket name
}

type RGWUser struct {
        name            string
        accessKey       string
        secret          string
}

type RGWClient struct {
        endpoint        string
        user            RGWUser
        client          *minio.Client
}

func (c *RGWClient) init() error {
        client, err := getS3Client(c.user, c.endpoint)

        if err != nil {
                return fmt.Errorf("getS3Client failed: %v", err)
        }
        c.client = client
        return nil
}

// Creates an bucket
func (c *RGWClient) createBucket(bucketName string) error {
	glog.Infof("Creating bucket %q", bucketName)
	location := "" // ignored for now...
	// create new bucket
	err := c.client.MakeBucket(bucketName, location)
	if err != nil {
		glog.Errorf("Error creating bucket: %v", err)
		return fmt.Errorf("S3-Client.MakeBucket err: %v", err)
	}
	glog.Infof("Create bucket %q succeeded.", bucketName)
	return nil
}

type broker struct {
	// rwMutex controls concurrent R and RW access
	rwMutex	    sync.RWMutex
	// instanceMap maps instanceIDs to the ID's userProvidedServiceInstance values
	instanceMap map[string]*s3ServiceInstance

        rgw         RGWClient

	uidPrefix   string
	gcUser      string

	// client used to access kubernetes
	kubeClient  *clientset.Clientset
}


type bucketInstance struct {
        user            RGWUser
        bucketName      string
}

// Initialize the rgw service broker. This function is called by `server.Start()`.
func CreateBroker() Broker {
	var instanceMap = make(map[string]*s3ServiceInstance)
	glog.Info("Generating new s3 broker.")

	// get the kubernetes client
	cs, err := getKubeClient()
	if err != nil {
		glog.Fatalf("failed to get kubernetes client: %v\n", err)
                return nil
	}

        client := RGWClient{}
        uidPrefix := "kube-rgw."
        gcUser := ""

        for _, e := range os.Environ() {
                pair := strings.Split(e, "=")
                switch pair[0] {
		case "S3_ENDPOINT":
                        client.endpoint = pair[1]
		case "S3_ACCESS_KEY":
                        client.user.accessKey = pair[1]
		case "S3_SECRET":
                        client.user.secret = pair[1]
		case "RGW_UID_PREFIX":
                        uidPrefix = pair[1]
		case "RGW_GC_USER":
                        gcUser = pair[1]
		}
        }

	// get the s3 client
	err = client.init()
	if err != nil {
		glog.Fatalf("failed to get s3 client: %v\n", err)
	}

        if gcUser == "" {
                gcUser = "rgw-kube-gc-user"
                _, err := client.provisionUser(gcUser, "rgw-broker-gc-" + gcUser, true)
                if err != nil {
                        glog.Fatalf("failed to create a user for broker gc: %v\n", err)
                        return nil
                }
        }

	glog.Infof("New Broker for s3 endpoint: %s", client.endpoint)
	return &broker{
		instanceMap: instanceMap,
		rgw:         client,
		kubeClient:  cs,
		uidPrefix:   uidPrefix,
                gcUser:      gcUser,
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

        userName := b.uidPrefix + xid.New().String()

        // First create a new user
        newUser, err := b.rgw.provisionUser(userName, "rgw-broker-instance-" + instanceID, false)
        if err != nil {
                return nil, err
        }

        newClient := RGWClient{}
        newClient.user = *newUser
        newClient.endpoint = b.rgw.endpoint
        err = newClient.init()
        if err != nil {
                glog.Errorf("Failed to init s3 client for new user: %v", err)
		return nil, fmt.Errorf("Failed to init s3 client for new user: %v", err)
        }

	if err := newClient.createBucket(bucketName); err != nil {
		return nil, err
	}
	b.instanceMap[instanceID] = &s3ServiceInstance{
		Namespace: req.ContextProfile.Namespace,
		Credential: brokerapi.Credential{
			USER_NAME:       newUser.name,
			BUCKET_NAME:     bucketName,
			BUCKET_ENDPOINT: newClient.endpoint,
                        ACCESS_KEY:      newUser.accessKey,
			SECRET_KEY:	 newUser.secret,
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
	userName := instance.Credential[USER_NAME].(string)
	bucketName := instance.Credential[BUCKET_NAME].(string)

        err := b.rgw.suspendUser(instance.Credential[USER_NAME].(string))
	if err != nil {
		glog.Errorf("Error failed to suspend user: %v", err)
		return nil, fmt.Errorf("Error failed to suspend user: %v", err)
	}

        bucketId, err := b.rgw.getBucketId(bucketName)
        if err != nil {
                return nil, fmt.Errorf("Error failed to retrieve bucket id for bucket %q: %v", bucketName, err)
        }
	glog.Infof("bucketId: %s", bucketId)

        err = b.rgw.unlinkBucket(userName, bucketName)
        if err != nil {
                return nil, fmt.Errorf("Error failed to unlink bucket %s/%s: %v", userName, bucketName, err)
        }

        err = b.rgw.linkBucket(b.gcUser, bucketName, bucketId)
        if err != nil {
                return nil, fmt.Errorf("Error failed to unlink bucket %s/%s: %v", userName, bucketName, err)
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

func (rgw *RGWClient) rgwAdminRequestRaw(method, section string, params url.Values) (*http.Response, error) {
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: http.DefaultTransport,
	}

        req_url := rgw.endpoint + "/admin/" + section + "?" + params.Encode()
        glog.Infof("sending http request: %s", req_url)

	req, err := http.NewRequest(method, req_url, bytes.NewReader(nil))
	if err != nil {
		glog.Errorf("Error creating http request params=%v", params)
		return nil, fmt.Errorf("Error creating http request params=%v", params)
	}

        req = s3signer.SignV4(*req, rgw.user.accessKey, rgw.user.secret, "", "")
	if req.Header.Get("Authorization") == "" {
		glog.Errorf("Error signing request: Authorization header is missing")
		return nil, fmt.Errorf("Error signing request: Authorization header is missing")
	}

        resp, err := httpClient.Do(req)
	if err != nil {
		glog.Errorf("Error sending http request: %v", err)
		return nil, fmt.Errorf("httpClient.Do err: %v", err)
	}

        return resp, nil
}

func (rgw *RGWClient) rgwAdminRequest(method, section string, params url.Values) ([]byte, error) {
        resp, err := rgw.rgwAdminRequestRaw(method, section, params)
        if err != nil {
                return nil, err
        }

        defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
                glog.Errorf("Error got http resonse: %v", resp.StatusCode)
                return nil, fmt.Errorf("Error got http response: %v", resp.StatusCode)
	}

        body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorf("Error reading response: %v", err)
		return nil, fmt.Errorf("Error reading response: %v", err)
	}

        return body, nil
}


func (rgw *RGWClient) provisionUser(userName, displayName string, successIfExists bool) (*RGWUser, error) {
	glog.Infof("Creating user %q", userName)

	// Set request parameters.
	params := make(url.Values)
	params.Set("uid", userName)
        params.Set("display-name", displayName)
        params.Set("key-type", "s3")
        params.Set("generate-key", "true")

        resp, err := rgw.rgwAdminRequestRaw("PUT", "user", params)
	if err != nil {
		glog.Errorf("Error creating user: %v", err)
		return nil, fmt.Errorf("Error creating user: %v", err)
	}
        defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && !(successIfExists && resp.StatusCode == 409) {
                glog.Errorf("Error got http resonse: %v", resp.StatusCode)
                return nil, fmt.Errorf("Error got http response: %v", resp.StatusCode)
	}

        params = make(url.Values)
	params.Set("uid", userName)
        body, err := rgw.rgwAdminRequest("GET", "user", params)
	if err != nil {
		glog.Errorf("Error fetching user info: %v", err)
                return nil, fmt.Errorf("Error fetching user info: %v", err)
	}

        type userInfo struct {
                UserId  string    `json:"user_id"`
                Keys    []struct {
                        AccessKey string  `json:"access_key"`
                        Secret string     `json:"secret_key"`
                } `json:"keys"`
        }

        res := userInfo{}
        err = json.Unmarshal(body, &res)
        if (err != nil) {
                glog.Errorf("Error failed to unmarshal user info: %v", err)
                return nil, fmt.Errorf("Error failed to unmarshal user info: %v", err)
        }

        if len(res.Keys) < 1 {
                glog.Errorf("Error access key wasn't generated for user %s", userName)
                return nil, fmt.Errorf("Error access key wasn't generated for user %s", userName)
        }

        glog.Infof("generated user %s (access_key=%s)", userName, res.Keys[0].AccessKey)

        user := new(RGWUser)
        user.name = userName
        user.accessKey = res.Keys[0].AccessKey
        user.secret = res.Keys[0].Secret

        return user, nil
}

func (rgw *RGWClient) getBucketId(bucketName string) (string, error) {
	glog.Infof("Getting bucket-id for buceket=%q", bucketName)

	// Set request parameters.
	params := make(url.Values)
        params.Set("key", "bucket:" + bucketName)

        body, err := rgw.rgwAdminRequest("GET", "metadata", params)
	if err != nil {
		glog.Errorf("Error fetching bucket metadata info: %v", err)
                return "", fmt.Errorf("Error fetching bucket metadata info: %v", err)
	}

        type bucketEntrypointInfo struct {
                Data struct {
                        Bucket  struct {
                                Name string     `json:"name"`
                                Marker string   `json:"marker"`
                                BucketId string `json:"bucket_id"`
                        } `json:"bucket"`
                } `json:"data"`
        }

        res := bucketEntrypointInfo{}
        err = json.Unmarshal(body, &res)
        if (err != nil) {
                glog.Errorf("Error failed to unmarshal bucket entrypoint info: %v", err)
                return "", fmt.Errorf("Error failed to unmarshal bucket entrypoint info: %v", err)
        }

        glog.Infof("retrieved bucket_id=%s)", res.Data.Bucket.BucketId)

        return res.Data.Bucket.BucketId, nil
}



func (rgw *RGWClient) unlinkBucket(userName, bucketName string) error {
	glog.Infof("Unlinking bucket %s/%s", userName, bucketName)

	// Set request parameters.
	params := make(url.Values)
        params.Set("uid", userName)
        params.Set("bucket", bucketName)

        _, err := rgw.rgwAdminRequest("POST", "bucket", params)
	if err != nil {
		glog.Errorf("Error unlinking bucket %s: %v", bucketName, err)
		return fmt.Errorf("Error unlinking bucket %s: %v", bucketName, err)
	}

        return nil
}

func (rgw *RGWClient) linkBucket(userName, bucketName, bucketId string) error {
	glog.Infof("Linking bucket %s/%s", userName, bucketName)

	// Set request parameters.
	params := make(url.Values)
        params.Set("uid", userName)
        params.Set("bucket", bucketName)
        params.Set("bucket-id", bucketId)

        _, err := rgw.rgwAdminRequest("PUT", "bucket", params)
	if err != nil {
		glog.Errorf("Error linking bucket %s: %v", bucketName, err)
		return fmt.Errorf("Error linking bucket %s: %v", bucketName, err)
	}

        return nil
}

func (rgw *RGWClient) suspendUser(userName string) error {
	glog.Infof("Suspending user %q", userName)

	// Set request parameters.
	params := make(url.Values)
	params.Set("uid", userName)
        params.Set("suspended", "true")

        _, err := rgw.rgwAdminRequest("POST", "user", params)
	if err != nil {
		glog.Errorf("Error creating user: %v", err)
		return fmt.Errorf("Error creating user: %v", err)
	}

        return nil
}


// Returns a minio api client.
func getS3Client(user RGWUser, endpoint string) (*minio.Client, error) {
	glog.Infof("Creating s3 client based on: \"%s\" on endpoint %s", user.accessKey, endpoint)

        addr := endpoint
        useSSL := false

        pair := strings.Split(endpoint, "://")

        if len(pair) > 1 {
	        useSSL = (pair[0] == "https")
                addr = pair[1]

        }

        glog.Infof("  addr=%s (ssl=%t)", addr, useSSL)

	minioClient, err := minio.NewV2(addr, user.accessKey, user.secret, useSSL)
	if err != nil {
		return nil, fmt.Errorf("Unable to create S3 client instance: %v", err)
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

