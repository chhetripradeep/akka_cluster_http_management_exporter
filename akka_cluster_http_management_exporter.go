package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
)

const (
	namespace = "akka" // For Prometheus metrics.
)

var (
	serverLabelNames = []string{"status"}
)

func newServerMetric(metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		serverLabelNames,
	)
}

type metrics map[int]*prometheus.GaugeVec

func (m metrics) String() string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	s := make([]string, len(keys))
	for i, k := range keys {
		s[i] = strconv.Itoa(k)
	}
	return strings.Join(s, ",")
}

var (
	serverMetrics = metrics{
		2: newServerMetric("current_members", "Current number of members of the akka cluster.", nil),
	}
)

type ClusterNode struct {
	Node    string
	NodeUid string
	Status  string
	Roles   []string
}

type Cluster struct {
	SelfNode    string
	Leader      string
	Oldest      string
	Unreachable []ClusterNode
	Members     []ClusterNode
}

// Exporter collects Akka Cluster HTTP stats from the given URI and exports them using
// the prometheus metrics package.
type Exporter struct {
	URI           string
	mutex         sync.RWMutex
	fetch         func() (io.ReadCloser, error)
	up            prometheus.Gauge
	serverMetrics map[int]*prometheus.GaugeVec
}

// NewExporter returns an initialized Exporter.
func NewExporter(uri string, timeout time.Duration) (*Exporter, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	var fetch func() (io.ReadCloser, error)
	switch u.Scheme {
	case "http", "https":
		fetch = fetchHTTP(uri, timeout)
	default:
		return nil, fmt.Errorf("unsupported scheme: %q", u.Scheme)
	}

	return &Exporter{
		URI:   uri,
		fetch: fetch,
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the last scrape of akka http management endpoint successful.",
		}),
		serverMetrics: serverMetrics,
	}, nil
}

// Describe describes all the metrics ever exported by the Akka HTTP Management Endpoint exporter.
// It implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.serverMetrics {
		m.Describe(ch)
	}
	ch <- e.up.Desc()
}

// Collect fetches the stats from configured Akka HTTP Management Endpoint and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()

	e.resetMetrics()
	e.scrape()

	ch <- e.up
	e.collectMetrics(ch)
}

func fetchHTTP(uri string, timeout time.Duration) func() (io.ReadCloser, error) {
	client := http.Client{
		Timeout: timeout,
	}

	return func() (io.ReadCloser, error) {
		resp, err := client.Get(uri)
		if err != nil {
			return nil, err
		}
		if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
}

func (e *Exporter) scrape() {
	body, err := e.fetch()
	if err != nil {
		e.up.Set(0)
		log.Errorf("Can't scrape akka http management endpoint: %v", err)
		return
	}
	defer body.Close()
	e.up.Set(1)

	var m Cluster

	if b, err := ioutil.ReadAll(body); err == nil {
		err = json.Unmarshal(b, &m)
		if err != nil {
			fmt.Println("error:", err)
		}
		e.exportJsonFields(e.serverMetrics, m.Members)
	}
}

// Expose Cluster Membership related metrics
// Akka Cluster Node States are referenced from here:
// 	http://doc.akka.io/docs/akka/2.5.3/images/member-states.png
func (e *Exporter) exportJsonFields(metrics map[int]*prometheus.GaugeVec, members []ClusterNode) {
	var joining, up, leaving, exiting, removed, down int
	for _, n := range members {
		switch n.Status {
		case "Up":
			up += 1
		case "Down":
			down += 1
		case "Joining":
			joining += 1
		case "Leaving":
			leaving += 1
		case "Exiting":
			exiting += 1
		case "Removed":
			removed += 1
		}
	}
	for _, metric := range metrics {
		metric.WithLabelValues("Up").Set(float64(up))
		metric.WithLabelValues("Down").Set(float64(down))
		metric.WithLabelValues("Joining").Set(float64(joining))
		metric.WithLabelValues("Leaving").Set(float64(leaving))
		metric.WithLabelValues("Exiting").Set(float64(exiting))
		metric.WithLabelValues("Removed").Set(float64(removed))
	}
}

func (e *Exporter) resetMetrics() {
	for _, m := range e.serverMetrics {
		m.Reset()
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.serverMetrics {
		m.Collect(metrics)
	}
}

func main() {
	var (
		listenAddress      = flag.String("web.listen-address", ":9110", "Address to listen on for web interface and telemetry.")
		metricsPath        = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		akkaProxyScrapeURI = flag.String("akka.scrape-uri", "http://localhost:19999/members", "URI on which to scrape Akka HTTP Endpoint.")
		akkaProxyTimeout   = flag.Duration("akka.timeout", 5*time.Second, "Timeout for trying to get stats from Akka HTTP Endpoint.")
		showVersion        = flag.Bool("version", false, "Print version information.")
	)
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, version.Print("akka_cluster_http_management_exporter"))
		os.Exit(0)
	}

	log.Infoln("Starting akka_cluster_http_management_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	exporter, err := NewExporter(*akkaProxyScrapeURI, *akkaProxyTimeout)
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(exporter)
	prometheus.MustRegister(version.NewCollector("akka_cluster_http_management_exporter"))

	log.Infoln("Listening on", *listenAddress)
	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Akka Cluster HTTP Management Exporter</title></head>
             <body>
             <h1>Akka Cluster HTTP Management Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
