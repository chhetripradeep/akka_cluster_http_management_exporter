FROM        quay.io/prometheus/busybox:latest
MAINTAINER  Pradeep Chhetri <pradeep@stashaway>

COPY akka_cluster_http_management_exporter_exporter /bin/akka_cluster_http_management_exporter

ENTRYPOINT ["/bin/akka_cluster_http_management_exporter"]
EXPOSE     9110
