package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/nu7hatch/gouuid"
	"golang.org/x/net/context"
)

// Node ...
type Node struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	MetaData   Meta   `json:"metadata"`
	Spec       Status `json:"spec"`
	NodeInfo   Info   `json:"nodeInfo,omitempty"`
}

// Meta ...
type Meta struct {
	IP   string `json:"ip"`
	UID  string `json:"uid"`
	Port string `json:"port"`
}

// Status ...
type Status struct {
	HostName string   `json:"hostname"`
	Cluster  string   `json:"cluster,omitempty"`
	Health   string   `json:"health,omitempty"`
	Role     []string `json:"role,omitempty"`
}

// Info ...
type Info struct {
	UserName string `json:"username,omitempty"`
	PassWord string `json:"password,omitempty"`
	Ping     string `json:"ping,omitempty"`
}

// Get info
func Get(kapi client.KeysAPI, path string, model *interface{}) (interface{}, error) {
	// get "/foo" key's value
	log.Printf("Getting %s key value", path)
	resp, err := kapi.Get(context.Background(), path, nil)

	if err != nil {
		return nil, err
	} else {
		// print common key info
		log.Printf("Get is done. Metadata is %q\n", resp)
		// print value
		err = json.Unmarshal([]byte(resp.Node.Value), model)
		log.Printf("%q key has %q value\n,struct is %#v", resp.Node.Key, resp.Node.Value, model)
	}
	return model, err
}

// Set info
func Set(kapi client.KeysAPI, path string, model *interface{}) error {
	Model, err := json.Marshal(model)
	if err != nil {
		log.Printf("Set:json.Marshal not done. err is %q\n", err)
		return err
	}
	resp, err := kapi.Set(context.Background(), path, string(Model), nil)
	if err == nil {

		log.Printf("Set is done. Metadata is %q\n", resp)
	}
	return err
}

// List info
func list(kapi client.KeysAPI, path string) []Node {
	resp, err := kapi.Get(context.Background(), path, nil)
	if err != nil {
		log.Printf("list not done. err is %q\n", err)
	}
	nodes := make([]Node, len(resp.Node.Nodes))
	nodeinfo := Node{}
	for index, n := range resp.Node.Nodes {
		err = json.Unmarshal([]byte(n.Value), &nodeinfo)
		nodes[index] = nodeinfo
	}
	if err != nil {
		log.Fatal(err)
	}
	return nodes

}

// Delete info
func Delete(kapi client.KeysAPI, path string) error {
	_, err := kapi.Delete(context.Background(), path, &client.DeleteOptions{Recursive: true})
	return err
}

func main() {
	cfg := client.Config{
		Endpoints: []string{"http://10.142.21.201:2379"},
		Transport: client.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}
	c, err := client.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	kapi := client.NewKeysAPI(c)
	uid, err := uuid.NewV4()
	if err != nil {
		log.Fatal(err)
	}
	auth := &Node{
		Kind:       "Node",
		APIVersion: "v1",
		MetaData: Meta{
			IP:   "10.142.21.15",
			UID:  fmt.Sprintf("%X", uid),
			Port: "22",
		},
		Spec: Status{
			HostName: "ys-1",
		},
	}
	log.Print("Setting '/config/auth' key with auth value")
	Auth, err := json.Marshal(auth)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := kapi.Set(context.Background(), "/config/auth", string(Auth), nil)
	if err != nil {
		log.Fatal(err)
	} else {
		// print  key info
		log.Printf("Set is done. Metadata is %q\n", resp)
	}
	// get "/foo" key's value
	log.Print("Getting '/foo' key value")
	resp, err = kapi.Get(context.Background(), "/config/auth", nil)
	nodeinfo := Node{}
	if err != nil {
		log.Fatal(err)
	} else {
		// print common key info
		log.Printf("Get is done. Metadata is %q\n", resp)
		// print value
		err = json.Unmarshal([]byte(resp.Node.Value), &nodeinfo)
		log.Printf("%q key has %q value\n,struct is %#v", resp.Node.Key, resp.Node.Value, nodeinfo)
	}
}
