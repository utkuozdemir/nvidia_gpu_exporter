FROM alpine:3

RUN apk add --no-cache tini
COPY nvidia-gpu-exporter /usr/local/bin/nvidia-gpu-exporter
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/nvidia-gpu-exporter"]
