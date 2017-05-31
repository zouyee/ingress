package etcd

import (
	"encoding/json"
	"log"

	"github.com/coreos/etcd/client"
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
func Get(kapi client.KeysAPI, path string) (Node, error) {
	// get "/foo" key's value
	log.Printf("Getting %s key value", path)
	resp, err := kapi.Get(context.Background(), path, nil)

	if err != nil {
		return Node{}, err
	}
	// print common key info
	log.Printf("Get is done. Metadata is %q\n", resp)
	// print value
	model := &Node{}
	err = json.Unmarshal([]byte(resp.Node.Value), model)

	return *model, err
}

// Set info
func Set(kapi client.KeysAPI, path string, model *Node) error {
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
func List(kapi client.KeysAPI, path string) ([]Node, error) {
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
		return nil, err
	}
	return nodes, err

}

// Delete info
func Delete(kapi client.KeysAPI, path string) error {
	_, err := kapi.Delete(context.Background(), path, &client.DeleteOptions{Recursive: true})
	return err
}
