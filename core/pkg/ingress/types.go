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

package ingress

import (
	"k8s.io/ingress/controllers/nginx/pkg/config"
	"k8s.io/ingress/core/pkg/ingress/defaults"
)

// Controller holds the methods to handle an Ingress backend
// TODO (#18): Make sure this is sufficiently supportive of other backends.
type Controller interface {

	// Reload takes a byte array representing the new loadbalancer configuration,
	// and returns a byte array containing any output/errors from the backend and
	// if a reload was required.
	// Before returning the backend must load the configuration in the given array
	// into the loadbalancer and restart it, or fail with an error and message string.
	// If reloading fails, there should be not change in the running configuration or
	// the given byte array.
	Reload(data []byte) ([]byte, bool, error)

	OnUpdate(config.Configuration) ([]byte, error)

	// SetListers allows the access of store listers present in the generic controller
	// This avoid the use of the kubernetes client.
	// BackendDefaults returns the minimum settings required to configure the
	// communication to endpoints
	BackendDefaults() defaults.Backend
	// Info returns information about the ingress controller
	Info() *BackendInfo
}

// BackendInfo returns information about the backend.
// This fields contains information that helps to track issues or to
// map the running ingress controller to source code
type BackendInfo struct {
	// Name returns the name of the backend implementation
	Name string `json:"name"`
	// Release returns the running version (semver)
	Release string `json:"release"`
	// Build returns information about the git commit
	Build string `json:"build"`
	// Repository return information about the git repository
	Repository string `json:"repository"`
}

// Backend describes one or more remote server/s (endpoints) associated with a service
type Backend struct {
	// Name represents an unique api.Service name formatted as <namespace>-<name>-<port>
	Name    string     `json:"name"`
	Cluster []Endpoint `json:"cluster"`
	Monitor Endpoint   `json:"monitor"`
	Alert   Endpoint   `json:"alert"`
	Log     Endpoint   `json:"log"`
}

// Endpoint ...
type Endpoint struct {
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
