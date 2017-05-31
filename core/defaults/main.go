package defaults

// Endpoint describes a node endpoint in a cluster
type Endpoint struct {
	// Address IP address of the endpoint
	Address string `json:"address"`
	// Port number of the TCP port
	Port string `json:"port"`
	// node in cluster role
	Role string `json:"role"`
}

// Backend defines the mandatory configuration that an Ingress controller must provide
// The reason of this requirements is the annotations are generic. If some implementation do not supports
// one or more annotations it just can provides defaults
type Backend struct {
	// AppRoot contains the AppRoot for apps that doesn't exposes its content in the 'root' context
	AppRoot string   `json:"cluster"`
	Auth    Endpoint `json:"auth"`
	Image   Endpoint `json:"image"`
}

