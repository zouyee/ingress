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

package config

import (
	"fmt"
	"runtime"
	"strconv"

	"github.com/zouyee/kube-cluster/core/pkg/types"
	"github.com/zouyee/kube-cluster/core/pkg/defaults"
)

const (
	// http://nginx.org/en/docs/http/ngx_http_core_module.html#client_max_body_size
	// Sets the maximum allowed size of the client request body
	bodySize = "1m"

	gzipTypes = "application/atom+xml application/javascript application/x-javascript application/json application/rss+xml application/vnd.ms-fontobject application/x-font-ttf application/x-web-app-manifest+json application/xhtml+xml application/xml font/opentype image/svg+xml image/x-icon text/css text/plain text/x-component"

	logFormatUpstream = `%v - [$proxy_add_x_forwarded_for] - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent" $request_length $request_time [$proxy_upstream_name] $upstream_addr $upstream_response_length $upstream_response_time $upstream_status`

	logFormatStream = `[$time_local] $protocol $status $bytes_sent $bytes_received $session_time`
)

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

// NewDefault returns the default nginx configuration
func NewDefault() Configuration {
	cfg := Configuration{
		GzipTypes:            gzipTypes,
		LogFormatUpstream:    logFormatUpstream,
		MaxWorkerConnections: 16384,
		WorkerProcesses:      strconv.Itoa(runtime.NumCPU()),
		Backend:              defaults.Backend{},
	}
	return cfg
}

// BuildLogFormatUpstream format the log_format upstream using
// proxy_protocol_addr as remote client address if UseProxyProtocol
// is enabled.
func (cfg Configuration) BuildLogFormatUpstream() string {
	if cfg.LogFormatUpstream == logFormatUpstream {
		return fmt.Sprintf(cfg.LogFormatUpstream, "$remote_addr")
	}

	return cfg.LogFormatUpstream
}

// TemplateConfig contains the nginx configuration to render the file nginx.conf
type TemplateConfig struct {
	MaxOpenFiles int

	Backends []*types.Backend

	HealthzURI string

	Cfg Configuration
}

