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

// BackendByNameServers sorts upstreams by name
type BackendByNameServers []*Backend

func (c BackendByNameServers) Len() int      { return len(c) }
func (c BackendByNameServers) Swap(i, j int) { c[i], c[j] = c[j], c[i] }
func (c BackendByNameServers) Less(i, j int) bool {

	return c[i].Name < c[j].Name
}

// EndpointByAddrPort sorts endpoints by address and port
type EndpointByAddrPort []Endpoint

func (c EndpointByAddrPort) Len() int      { return len(c) }
func (c EndpointByAddrPort) Swap(i, j int) { c[i], c[j] = c[j], c[i] }
func (c EndpointByAddrPort) Less(i, j int) bool {
	iName := c[i].Address
	jName := c[j].Address
	if iName != jName {
		return iName < jName
	}

	iU := c[i].Port
	jU := c[j].Port
	return iU < jU
}
