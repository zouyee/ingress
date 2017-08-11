package config

import (
	"runtime"
	"strconv"

	"github.com/zouyee/kube-cluster/core/defaults"
	"github.com/zouyee/kube-cluster/core/types"
)

const (
	// http://nginx.org/en/docs/http/ngx_http_core_module.html#client_max_body_size
	// Sets the maximum allowed size of the client request body
	bodySize = "1m"

	gzipTypes = "application/atom+xml application/javascript application/x-javascript application/json application/rss+xml application/vnd.ms-fontobject application/x-font-ttf application/x-web-app-manifest+json application/xhtml+xml application/xml font/opentype image/svg+xml image/x-icon text/css text/plain text/x-component"

	logFormatUpstream = `%v - [$proxy_add_x_forwarded_for] - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent" $request_length $request_time [$proxy_upstream_name] $upstream_addr $upstream_response_length $upstream_response_time $upstream_status`

	logFormatStream = `[$time_local] $protocol $status $bytes_sent $bytes_received $session_time`
)

// NewDefault returns the default nginx configuration
func NewDefault() types.Configuration {
	cfg := types.Configuration{
		GzipTypes:            gzipTypes,
		LogFormatUpstream:    logFormatUpstream,
		MaxWorkerConnections: 16384,
		WorkerProcesses:      strconv.Itoa(runtime.NumCPU()),
		Backend:              defaults.Backend{},
	}
	return cfg
}

// TemplateConfig contains the nginx configuration to render the file nginx.conf
type TemplateConfig struct {
	MaxOpenFiles int

	Backends *types.Backend

	Cfg types.Configuration
}
