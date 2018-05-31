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
        "encoding/binary"
	"crypto/rand"
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

type rgwServiceInstance struct {
	// k8s namespace
	Namespace string
        Endpoint string
	UserName string
        BucketName string
}

type rgwBindInfo struct {
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
	instanceMap map[string]*rgwServiceInstance

        rgw         RGWClient

	uidPrefix   string
	gcUser      string
	dataBucket  string

	// client used to access kubernetes
	kubeClient  *clientset.Clientset
}


type bucketInstance struct {
        user            RGWUser
        bucketName      string
}

// Initialize the rgw service broker. This function is called by `server.Start()`.
func CreateBroker() Broker {
	var instanceMap = make(map[string]*rgwServiceInstance)
	glog.Info("Generating new Ceph rgw object broker.")

	// get the kubernetes client
	cs, err := getKubeClient()
	if err != nil {
		glog.Fatalf("failed to get kubernetes client: %v\n", err)
                return nil
	}

        client := RGWClient{}
        uidPrefix := "kube-rgw."
        dataBucket := "kube-rgw-data"
        gcUser := ""

        for _, e := range os.Environ() {
                pair := strings.Split(e, "=")
                switch pair[0] {
		case "RGW_ENDPOINT":
                        client.endpoint = pair[1]
		case "RGW_ACCESS_KEY":
                        client.user.accessKey = pair[1]
		case "RGW_SECRET":
                        client.user.secret = pair[1]
		case "RGW_UID_PREFIX":
                        uidPrefix = pair[1]
		case "RGW_GC_USER":
                        gcUser = pair[1]
		case "RGW_DATA_BUCKET":
                        dataBucket = pair[1]
		}
        }

	// get the s3 client
	err = client.init()
	if err != nil {
		glog.Fatalf("failed to get s3 client: %v\n", err)
	}

        if gcUser == "" {
                gcUser = "rgw-kube-gc-user"
                _, err := client.provisionUser(gcUser, "rgw-broker-gc-" + gcUser, false, true)
                if err != nil {
                        glog.Fatalf("failed to create a user for broker gc: %v\n", err)
                        return nil
                }
        }

        err = client.createBucket(dataBucket)
        if (err != nil) {
                glog.Fatalf("Error: failed to create bucket %s", dataBucket)
                return nil
        }

	glog.Infof("New Broker for rgw endpoint: %s", client.endpoint)
	return &broker{
		instanceMap: instanceMap,
		rgw:         client,
		kubeClient:  cs,
		uidPrefix:   uidPrefix,
                gcUser:      gcUser,
                dataBucket:  dataBucket,
	}
}
// Implements the `Catalog` interface method.
func (b *broker) Catalog() (*brokerapi.Catalog, error) {
	return &brokerapi.Catalog{
		Services: []*brokerapi.Service{
			{
				Name:        "rgw-bucket-service",
				ID:          "0",
				Description: "A bucket of storage object backed by Ceph RGW.",
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

func (b *broker) findInstance(instanceID string) (*rgwServiceInstance, error) {
	instance, ok := b.instanceMap[instanceID]
	if !ok {
                var err error
                instance, err = b.getInstanceInfo(instanceID)
                if err != nil {
                        return nil, retErrInfof("InstanceID %q not found.", instanceID)
                }
	}
        return instance, nil
}

// The `GetServiceInstanceLastOperation` interface method is not implemented.
func (b *broker) GetServiceInstanceLastOperation(instanceID, serviceID, planID, operation string) (*brokerapi.LastOperationResponse, error) {
	glog.Info("GetServiceInstanceLastOperation not yet implemented.")
	return nil, nil
}

// Implements the `CreateServiceInstance` interface method by creating (provisioning) a bucket.
// Note: (nil, nil) is returned for success, meaning the CreateServiceInstanceResponse is ignored by
//   the caller.
func (b *broker) CreateServiceInstance(instanceID string, req *brokerapi.CreateServiceInstanceRequest) (*brokerapi.CreateServiceInstanceResponse, error) {
	glog.Infof("CreateServiceInstance called.  instanceID: %s", instanceID)
	b.rwMutex.Lock()
	defer b.rwMutex.Unlock()
	// does service instance exist?

        _, err := b.findInstance(instanceID)
	if err == nil {
		return nil, retErrInfof("Instance requested already exists.")
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
        newUser, err := b.rgw.provisionUser(userName, "rgw-broker-instance-" + instanceID, true, false)
        if err != nil {
                return nil, err
        }

        newClient := RGWClient{
                user: *newUser,
                endpoint: b.rgw.endpoint,
        }
        err = newClient.init()
        if err != nil {
                glog.Errorf("Failed to init s3 client for new user: %v", err)
		return nil, fmt.Errorf("Failed to init s3 client for new user: %v", err)
        }

	if err := newClient.createBucket(bucketName); err != nil {
		return nil, err
	}

	if err := b.rgw.modifyUser(userName, "max-buckets", "-1"); err != nil {
		return nil, err
	}

        instanceInfo := rgwServiceInstance{
		Namespace: req.ContextProfile.Namespace,
                Endpoint: newClient.endpoint,
                UserName: newUser.name,
                BucketName: bucketName,
	}

        err = b.storeInstanceInfo(instanceID, instanceInfo)
        if (err != nil) {
                return nil, retErrInfof("Error: failed to store instance info: %s", err)
        }

	b.instanceMap[instanceID] = &instanceInfo

	return nil, nil
}

// Implements the `RemoveServiceInstance` interface method.
func (b *broker) RemoveServiceInstance(instanceID, serviceID, planID string, acceptsIncomplete bool) (*brokerapi.DeleteServiceInstanceResponse, error) {
	glog.Infof("RemoveServiceInstance called. instanceID: %s", instanceID)
	b.rwMutex.Lock()
	defer b.rwMutex.Unlock()
        instance, err := b.findInstance(instanceID)
	if err != nil {
                glog.Errorf("InstanceID %q not found.", instanceID)
                /* don't return error, if it wasn't found it was already removed */
                return nil, nil
	}

        userName := instance.UserName
        bucketName := instance.BucketName

        err = b.rgw.suspendUser(userName)
	if err != nil {
		glog.Errorf("Error failed to suspend user: %v", err)
		return nil, fmt.Errorf("Error failed to suspend user: %v", err)
	}

        var status int

        bucketId, err := b.rgw.getBucketId(bucketName, &status)
        if status != http.StatusNotFound {
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
        }

        err = b.removeInstanceInfo(instanceID)
        if err != nil {
                glog.Infof("Warning: failed to clean instance info: instanceID=%s: %s", instanceID, err)
        }

	delete(b.instanceMap, BUCKET_NAME)
	glog.Infof("Remove bucket %q succeeded.", bucketName)
	return nil, nil
}

// Implements the `Bind` interface method.
func (b *broker) Bind(instanceID, bindingID string, req *brokerapi.BindingRequest) (*brokerapi.CreateServiceBindingResponse, error) {
	glog.Infof("Bind called. instanceID: %q", instanceID)
        instance, err := b.findInstance(instanceID)
	if err != nil {
		return nil, fmt.Errorf("Instance ID %q not found.", instanceID)
	}

	if instance.UserName == "" {
		return nil, retErrInfof("No user found for instance %q.", instanceID)
	}

        oldInfo, err := b.getBindInfo(instanceID, bindingID)
        if err == nil {
                glog.Infof("Bind ID already exists, returning existing info")
                return &brokerapi.CreateServiceBindingResponse{
                        Credentials: oldInfo.Credential,
                }, nil
        }

        key, err := b.rgw.createKey(instance.UserName)
        if err != nil {
                return nil, retErrInfof("Error: failed to create access key: %s", err)
        }
        creds := brokerapi.Credential{
                USER_NAME:       instance.UserName,
                BUCKET_NAME:     instance.BucketName,
                BUCKET_ENDPOINT: instance.Endpoint,
                ACCESS_KEY:      key.accessKey,
                SECRET_KEY:      key.secret,
        }

        bInfo := rgwBindInfo {
                Credential: creds,
        }

        err = b.storeBindInfo(instanceID, bindingID, bInfo)


	glog.Infof("Bind instance %q succeeded.", instanceID)
	return &brokerapi.CreateServiceBindingResponse{
		Credentials: creds,
	}, nil
}

// nothing to do here
// The `UnBind` interface method is not implemented.
func (b *broker) UnBind(instanceID, bindingID, serviceID, planID string) error {
        glog.Infof("Bind called. instanceID: %q, bindingID: %q", instanceID, bindingID)
        instance, err := b.findInstance(instanceID)
	if err != nil {
		glog.Infof("Instance ID %q not found.", instanceID)
                /* don't return error */
		return nil
	}


        oldInfo, err := b.getBindInfo(instanceID, bindingID)
        if err != nil {
                glog.Infof("Bind ID not found, assume was already removed")
                return nil
        }

        err = b.rgw.removeKey(instance.UserName, oldInfo.Credential[ACCESS_KEY].(string))
        if err != nil {
                glog.Infof("Failed to remove access key")
                return err
        }

        err = b.removeBindInfo(instanceID, bindingID)
        if err != nil {
                glog.Infof("Failed to remove binding info")
                return nil
        }
	return nil
}

func (b *broker) storeInfo(oid string, object interface{}) error {
	data, err := json.Marshal(object)
	if err != nil {
                return retErrInfof("Error failed to marshal object %s/%s: %s", b.dataBucket, oid, err)
	}

        n, err := b.rgw.client.PutObject(b.dataBucket, oid, bytes.NewReader(data), "application/json")
        if err != nil {
                return retErrInfof("Error failed to PutObject() %s/%s", b.dataBucket, oid)
        }
        if n != int64(len(data)) {
                return retErrInfof("Error PutObject() unexpected num of bytes written %s/%s: expected %d wrote %d", b.dataBucket, oid, len(data), n)
        }
        return nil
}

func (b *broker) readInfo(oid string, object interface{}) error {
        r, err := b.rgw.client.GetObject(b.dataBucket, oid)
        if err != nil {
                return retErrInfof("Error failed to GetObject() %s/%s: %s", b.dataBucket, oid, err)
        }

	buf, err := ioutil.ReadAll(r)
	if err != nil {
                return retErrInfof("Error failed to ReadAll() %s/%s: %s", b.dataBucket, oid, err)
	}

	err = json.Unmarshal(buf, &object)
	if err != nil {
                return retErrInfof("Error failed to unmarshal object %s/%s: %s", b.dataBucket, oid, err)
	}
        return nil
}

func (b *broker) removeInfo(oid string) error {
        err := b.rgw.client.RemoveObject(b.dataBucket, oid)
        if err != nil {
                return retErrInfof("Error failed to RemoveObject() %s/%s: %s", b.dataBucket, oid, err)
        }
        return nil
}

func getInstanceOid(instanceId string) string {
        return "instance/" + instanceId
}

func getBindOid(instanceId, bindId string) string {
        return "bind/" + instanceId + "/" + bindId
}

func (b *broker) storeInstanceInfo(id string, info rgwServiceInstance) error {
        return b.storeInfo(getInstanceOid(id), info)
}

func (b *broker) getInstanceInfo(id string) (*rgwServiceInstance, error) {
        info := new(rgwServiceInstance)
        err := b.readInfo(getInstanceOid(id), info)
        return info, err
}

func (b *broker) removeInstanceInfo(id string) error {
        return b.removeInfo(getInstanceOid(id))
}

func (b *broker) storeBindInfo(instanceId, bindId string, info rgwBindInfo) error {
        return b.storeInfo(getBindOid(instanceId, bindId), info)
}

func (b *broker) getBindInfo(instanceId, bindId string) (*rgwBindInfo, error) {
        info := new(rgwBindInfo)
        err := b.readInfo(getBindOid(instanceId, bindId), info)
        return info, err
}

func (b *broker) removeBindInfo(instanceId, bindId string) error {
        return b.removeInfo(getBindOid(instanceId, bindId))
}

func (rgw *RGWClient) rgwAdminRequestRaw(method, section, resource string, params url.Values) (*http.Response, error) {
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: http.DefaultTransport,
	}

        resourceStr := ""
        if resource != "" {
                resourceStr = resource + "&"
        }

        req_url := rgw.endpoint + "/admin/" + section + "?" + resourceStr + params.Encode()
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

func (rgw *RGWClient) rgwAdminRequest(method, section, resource string, params url.Values, status *int) ([]byte, error) {
        resp, err := rgw.rgwAdminRequestRaw(method, section, resource, params)
        if err != nil {
                return nil, err
        }

        defer resp.Body.Close()

        if status != nil {
                *status = resp.StatusCode
        }

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

type userInfo struct {
        UserId  string    `json:"user_id"`
        Keys    []struct {
                AccessKey string  `json:"access_key"`
                Secret string     `json:"secret_key"`
        } `json:"keys"`
}

func (rgw *RGWClient) getUserInfo(userName string) (*userInfo, error) {
        params := make(url.Values)
	params.Set("uid", userName)
        body, err := rgw.rgwAdminRequest("GET", "user", "", params, nil)
	if err != nil {
		glog.Errorf("Error fetching user info: %v", err)
                return nil, fmt.Errorf("Error fetching user info: %v", err)
	}

        userInfo := new(userInfo)
        err = json.Unmarshal(body, userInfo)
        if (err != nil) {
                glog.Errorf("Error failed to unmarshal user info: %v", err)
                return nil, fmt.Errorf("Error failed to unmarshal user info: %v", err)
        }

        return userInfo, nil
}

func (rgw *RGWClient) provisionUser(userName, displayName string, genAccessKey, successIfExists bool) (*RGWUser, error) {
	glog.Infof("Creating user %q", userName)

	// Set request parameters.
	params := make(url.Values)
	params.Set("uid", userName)
        params.Set("display-name", displayName)
        params.Set("key-type", "s3")
        if genAccessKey {
                params.Set("generate-key", "true")
        } else {
                params.Set("generate-key", "false")
        }

        resp, err := rgw.rgwAdminRequestRaw("PUT", "user", "", params)
	if err != nil {
		glog.Errorf("Error creating user: %v", err)
		return nil, fmt.Errorf("Error creating user: %v", err)
	}
        defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && !(successIfExists && resp.StatusCode == 409) {
                glog.Errorf("Error got http resonse: %v", resp.StatusCode)
                return nil, fmt.Errorf("Error got http response: %v", resp.StatusCode)
	}

        uInfo, err := rgw.getUserInfo(userName)
        if (err != nil) {
                return nil, err
        }

        user := new(RGWUser)
        user.name = userName

        if genAccessKey {
                if len(uInfo.Keys) < 1 {
                        glog.Errorf("Error access key wasn't generated for user %s", userName)
                        return nil, fmt.Errorf("Error access key wasn't generated for user %s", userName)
                }

                glog.Infof("generated user %s (access_key=%s)", userName, uInfo.Keys[0].AccessKey)

                user.accessKey = uInfo.Keys[0].AccessKey
                user.secret = uInfo.Keys[0].Secret
        }

        return user, nil
}

func (rgw *RGWClient) modifyUser(userName, param, val string) error {
	glog.Infof("Modifying user user %q (%s=%s)", userName, param, val)

	// Set request parameters.
	params := make(url.Values)
	params.Set("uid", userName)
	params.Set(param, val)

        _, err := rgw.rgwAdminRequest("POST", "user", "", params, nil)
	if err != nil {
		return retErrInfof("Error modifying user: %v", err)
	}

        return nil
}

func (rgw *RGWClient) getBucketId(bucketName string, status *int) (string, error) {
	glog.Infof("Getting bucket-id for buceket=%q", bucketName)

	// Set request parameters.
	params := make(url.Values)
        params.Set("key", "bucket:" + bucketName)

        body, err := rgw.rgwAdminRequest("GET", "metadata", "", params, status)
	if err != nil {
                return "", retErrInfof("Error fetching bucket metadata info: %v", err)
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
                return "", retErrInfof("Error failed to unmarshal bucket entrypoint info: %v", err)
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

        _, err := rgw.rgwAdminRequest("POST", "bucket", "", params, nil)
	if err != nil {
		glog.Errorf("Error unlinking bucket %s: %v", bucketName, err)
		return fmt.Errorf("Error unlinking bucket %s: %v", bucketName, err)
	}

        return nil
}

func (rgw *RGWClient) linkBucket(userName, bucketName, bucketId string) error {
	glog.Infof("Linking bucket %s/%s to user %s", userName, bucketName, userName)

	// Set request parameters.
	params := make(url.Values)
        params.Set("uid", userName)
        params.Set("bucket", bucketName)
        params.Set("bucket-id", bucketId)

        _, err := rgw.rgwAdminRequest("PUT", "bucket", "", params, nil)
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

        _, err := rgw.rgwAdminRequest("POST", "user", "", params, nil)
	if err != nil {
		glog.Errorf("Error creating user: %v", err)
		return fmt.Errorf("Error creating user: %v", err)
	}

        return nil
}

var alphaChars = []rune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")

func getRand(max int) (int, error) {
        buf := make([]byte, 4)
        _, err := rand.Read(buf)
        if err != nil {
                return -1, err
        }

        val := binary.BigEndian.Uint32(buf)

        return int(val) % max, nil
}


func getRandAlpha(size int) (string, error) {
        buf := make([]rune, size)
        for i := range buf {
                r, err := getRand(len(alphaChars))
                if err != nil {
                        return "", retErrInfof("Error: failed to generate random number: %s", err)
                }
                buf[i] = alphaChars[r]
        }
        return string(buf), nil
}

func (rgw *RGWClient) createKey(userName string) (*RGWUser, error) {
	glog.Infof("Creating new key for user %q", userName)


        accessKey, err := getRandAlpha(20)
        if err != nil {
                return nil, retErrInfof("Error failed to generate access key: %s", err)
        }

	// Set request parameters.
	params := make(url.Values)
	params.Set("uid", userName)
        params.Set("access-key", accessKey)
        params.Set("key-type", "s3")
        params.Set("generate-key", "true")

        _, err = rgw.rgwAdminRequest("PUT", "user", "key", params, nil)
	if err != nil {
		return nil, retErrInfof("Error generating access key: %v", err)
	}

        uInfo, err := rgw.getUserInfo(userName)
        if (err != nil) {
                return nil, err
        }

        secret := ""

        for _, k := range uInfo.Keys {
                if k.AccessKey == accessKey {
                        secret = k.Secret
                        break
                }
        }

        if secret == "" {
                return nil, retErrInfof("Error: can't find generated access key (user=%s access_key=%s)", userName, accessKey)
        }

        user := new(RGWUser)
        user.name = userName
        user.accessKey = accessKey
        user.secret = secret

        return user, nil
}

func (rgw *RGWClient) removeKey(userName, accessKey string) error {
        glog.Infof("Removing accessKey %s:%s", userName, accessKey)

	// Set request parameters.
	params := make(url.Values)
	params.Set("uid", userName)
        params.Set("access-key", accessKey)

        _, err := rgw.rgwAdminRequest("DELETE", "user", "key", params, nil)
	if err != nil {
		return retErrInfof("Error removing access key: %v", err)
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


func retErrInfof(format string, args ...interface{}) error {
        glog.Infof(format, args)
        return fmt.Errorf(format, args)
}
