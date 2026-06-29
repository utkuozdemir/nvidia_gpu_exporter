FROM ubuntu:26.04

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
