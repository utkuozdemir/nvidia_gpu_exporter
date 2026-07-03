FROM debian:13-slim

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
