package types

import (
	"fmt"

	"github.com/zouyee/kube-cluster/core/defaults"
)

const logFormatUpstream = `%v - [$proxy_add_x_forwarded_for] - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent" $request_length $request_time [$proxy_upstream_name] $upstream_addr $upstream_response_length $upstream_response_time $upstream_status`

// Configuration represents the content of nginx.conf file
type Configuration struct {
	defaults.Backend `json:",squash"`

	// Customize upstream log_format
	// http://nginx.org/en/docs/http/ngx_http_log_module.html#log_format
	LogFormatUpstream string `json:"log-format-upstream,omitempty"`

	// Maximum number of simultaneous connections that can be opened by each worker process
	// http://nginx.org/en/docs/ngx_core_module.html#worker_connections
	MaxWorkerConnections int `json:"max-worker-connections,omitempty"`

	// MIME types in addition to "text/html" to compress. The special value “*” matches any MIME type.
	// Responses with the “text/html” type are always compressed if UseGzip is enabled
	GzipTypes string `json:"gzip-types,omitempty"`

	// Defines the number of worker processes. By default auto means number of available CPU cores
	// http://nginx.org/en/docs/ngx_core_module.html#worker_processes
	WorkerProcesses string `json:"worker-processes,omitempty"`
}

// Controller ...
type Controller interface {
	Reload(data []byte) ([]byte, bool, error)

	OnUpdate(Configuration) ([]byte, error)

	BackendDefaults() defaults.Backend

	Info() BackendInfo
}

// BuildLogFormatUpstream ...
func (cfg Configuration) BuildLogFormatUpstream() string {
	if cfg.LogFormatUpstream == logFormatUpstream {
		return fmt.Sprintf(cfg.LogFormatUpstream, "$remote_addr")
	}

	return cfg.LogFormatUpstream
}

// BackendInfo ...
type BackendInfo struct {
	Name string `json:"name"`

	Release string `json:"release"`

	Build string `json:"build"`
}

// Backend ...
type Backend struct {
	Name    string                 `json:"name"`
	Cluster map[string][]*Endpoint `json:"cluster"`
	Auth    Endpoint               `json:"auth"`
	Image   Endpoint               `json:"image"`
}

// Endpoint ...
type Endpoint struct {
	Name     string `json:"name"`
	Cluster  string `json:"cluster"`
	Address  string `json:"address"`
	Port     string `json:"port"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// PostBackend ...
type PostBackend struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Cluster     []*Endpoint `json:"cluster"`
	Auth        Endpoint    `json:"auth"`
	Image       Endpoint    `json:"image"`
	Status      string      `json:"status"`
}
