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

package collector

import (
	"reflect"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
)

const system = "nginx"

type (
	vtsCollector struct {
		scrapeChan    chan scrapeRequest
		ngxHealthPort int
		ngxVtsPath    string
		data          *vtsData
	}

	vtsData struct {
		bytes                *prometheus.Desc
		cache                *prometheus.Desc
		connections          *prometheus.Desc
		response             *prometheus.Desc
		request              *prometheus.Desc
		filterZoneBytes      *prometheus.Desc
		filterZoneResponse   *prometheus.Desc
		filterZoneCache      *prometheus.Desc
		upstreamBackup       *prometheus.Desc
		upstreamBytes        *prometheus.Desc
		upstreamDown         *prometheus.Desc
		upstreamFailTimeout  *prometheus.Desc
		upstreamMaxFails     *prometheus.Desc
		upstreamResponses    *prometheus.Desc
		upstreamRequest      *prometheus.Desc
		upstreamResponseMsec *prometheus.Desc
		upstreamWeight       *prometheus.Desc
	}
)

// NewNGINXVTSCollector returns a new prometheus collector for the VTS module
func NewNGINXVTSCollector(namespace, class string, ngxHealthPort int, ngxVtsPath string) Stopable {
	p := vtsCollector{
		scrapeChan:    make(chan scrapeRequest),
		ngxHealthPort: ngxHealthPort,
		ngxVtsPath:    ngxVtsPath,
	}

	ns := buildNS(system, namespace, class)

	p.data = &vtsData{
		bytes: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "bytes_total"),
			"Nginx bytes count",
			[]string{"server_zone", "direction"}, nil),

		cache: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "cache_total"),
			"Nginx cache count",
			[]string{"server_zone", "type"}, nil),

		connections: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "connections_total"),
			"Nginx connections count",
			[]string{"type"}, nil),

		response: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "responses_total"),
			"The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.",
			[]string{"server_zone", "status_code"}, nil),

		request: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "requests_total"),
			"The total number of requested client connections.",
			[]string{"server_zone"}, nil),

		filterZoneBytes: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "filterzone_bytes_total"),
			"Nginx bytes count",
			[]string{"server_zone", "country", "direction"}, nil),

		filterZoneResponse: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "filterzone_responses_total"),
			"The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.",
			[]string{"server_zone", "country", "status_code"}, nil),

		filterZoneCache: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "filterzone_cache_total"),
			"Nginx cache count",
			[]string{"server_zone", "country", "type"}, nil),

		upstreamBackup: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_backup"),
			"Current backup setting of the server.",
			[]string{"upstream", "server"}, nil),

		upstreamBytes: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_bytes_total"),
			"The total number of bytes sent to this server.",
			[]string{"upstream", "server", "direction"}, nil),

		upstreamDown: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "vts_upstream_down_total"),
			"Current down setting of the server.",
			[]string{"upstream", "server"}, nil),

		upstreamFailTimeout: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_fail_timeout"),
			"Current fail_timeout setting of the server.",
			[]string{"upstream", "server"}, nil),

		upstreamMaxFails: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_maxfails"),
			"Current max_fails setting of the server.",
			[]string{"upstream", "server"}, nil),

		upstreamResponses: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_responses_total"),
			"The number of upstream responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.",
			[]string{"upstream", "server", "status_code"}, nil),

		upstreamRequest: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_requests_total"),
			"The total number of client connections forwarded to this server.",
			[]string{"upstream", "server"}, nil),

		upstreamResponseMsec: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_response_msecs_avg"),
			"The average of only upstream response processing times in milliseconds.",
			[]string{"upstream", "server"}, nil),

		upstreamWeight: prometheus.NewDesc(
			prometheus.BuildFQName(system, ns, "upstream_weight"),
			"Current upstream weight setting of the server.",
			[]string{"upstream", "server"}, nil),
	}

	go p.start()

	return p
}

// Describe implements prometheus.Collector.
func (p vtsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- p.data.bytes
	ch <- p.data.cache
	ch <- p.data.connections
	ch <- p.data.request
	ch <- p.data.response
	ch <- p.data.upstreamBackup
	ch <- p.data.upstreamBytes
	ch <- p.data.upstreamDown
	ch <- p.data.upstreamFailTimeout
	ch <- p.data.upstreamMaxFails
	ch <- p.data.upstreamRequest
	ch <- p.data.upstreamResponseMsec
	ch <- p.data.upstreamResponses
	ch <- p.data.upstreamWeight
	ch <- p.data.filterZoneBytes
	ch <- p.data.filterZoneCache
	ch <- p.data.filterZoneResponse
}

// Collect implements prometheus.Collector.
func (p vtsCollector) Collect(ch chan<- prometheus.Metric) {
	req := scrapeRequest{results: ch, done: make(chan struct{})}
	p.scrapeChan <- req
	<-req.done
}

func (p vtsCollector) start() {
	for req := range p.scrapeChan {
		ch := req.results
		p.scrapeVts(ch)
		req.done <- struct{}{}
	}
}

func (p vtsCollector) Stop() {
	close(p.scrapeChan)
}

// scrapeVts scrape nginx vts metrics
func (p vtsCollector) scrapeVts(ch chan<- prometheus.Metric) {
	nginxMetrics, err := getNginxVtsMetrics(p.ngxHealthPort, p.ngxVtsPath)
	if err != nil {
		glog.Warningf("unexpected error obtaining nginx status info: %v", err)
		return
	}

	reflectMetrics(&nginxMetrics.Connections, p.data.connections, ch)

	for name, zones := range nginxMetrics.UpstreamZones {
		for pos, value := range zones {
			reflectMetrics(&zones[pos].Responses, p.data.upstreamResponses, ch, name, value.Server)

			ch <- prometheus.MustNewConstMetric(p.data.upstreamRequest,
				prometheus.CounterValue, zones[pos].RequestCounter, name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamDown,
				prometheus.CounterValue, float64(zones[pos].Down), name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamWeight,
				prometheus.CounterValue, zones[pos].Weight, name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamResponseMsec,
				prometheus.CounterValue, zones[pos].ResponseMsec, name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamBackup,
				prometheus.CounterValue, float64(zones[pos].Backup), name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamFailTimeout,
				prometheus.CounterValue, zones[pos].FailTimeout, name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamMaxFails,
				prometheus.CounterValue, zones[pos].MaxFails, name, value.Server)
			ch <- prometheus.MustNewConstMetric(p.data.upstreamBytes,
				prometheus.CounterValue, zones[pos].InBytes, name, value.Server, "in")
			ch <- prometheus.MustNewConstMetric(p.data.upstreamBytes,
				prometheus.CounterValue, zones[pos].OutBytes, name, value.Server, "out")
		}
	}

	for name, zone := range nginxMetrics.ServerZones {
		reflectMetrics(&zone.Responses, p.data.response, ch, name)
		reflectMetrics(&zone.Cache, p.data.cache, ch, name)

		ch <- prometheus.MustNewConstMetric(p.data.request,
			prometheus.CounterValue, zone.RequestCounter, name)
		ch <- prometheus.MustNewConstMetric(p.data.bytes,
			prometheus.CounterValue, zone.InBytes, name, "in")
		ch <- prometheus.MustNewConstMetric(p.data.bytes,
			prometheus.CounterValue, zone.OutBytes, name, "out")
	}

	for serverZone, countries := range nginxMetrics.FilterZones {
		for country, zone := range countries {
			reflectMetrics(&zone.Responses, p.data.filterZoneResponse, ch, serverZone, country)
			reflectMetrics(&zone.Cache, p.data.filterZoneCache, ch, serverZone, country)

			ch <- prometheus.MustNewConstMetric(p.data.filterZoneBytes,
				prometheus.CounterValue, float64(zone.InBytes), serverZone, country, "in")
			ch <- prometheus.MustNewConstMetric(p.data.filterZoneBytes,
				prometheus.CounterValue, float64(zone.OutBytes), serverZone, country, "out")
		}
	}
}

func reflectMetrics(value interface{}, desc *prometheus.Desc, ch chan<- prometheus.Metric, labels ...string) {
	val := reflect.ValueOf(value).Elem()

	for i := 0; i < val.NumField(); i++ {
		tag := val.Type().Field(i).Tag
		l := append(labels, tag.Get("json"))
		ch <- prometheus.MustNewConstMetric(desc,
			prometheus.CounterValue, float64(val.Field(i).Interface().(float64)),
			l...)
	}
}
