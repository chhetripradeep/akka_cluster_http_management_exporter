# Akka Cluster HTTP Management Exporter for Prometheus

This is a simple server that scrapes Akka HTTP Management stats and exports them via HTTP for
Prometheus consumption.

## Getting Started

To run it:

```bash
./akka_cluster_http_management_exporter [flags]
```

Help on flags:

```bash
./akka_cluster_http_management_exporter --help
```

## Usage

### HTTP stats URL

Specify custom URLs for the Akka Cluster HTTP Management stats URI using the `-scrape-uri` flag.

```bash
akka_cluster_http_management_exporter -akka.scrape-uri="http://localhost:19999/members"
```

Or to scrape a remote host:

```bash
akka_cluster_http_management_exporter -akka.scrape-uri="http://example.com:19999/members"
```
