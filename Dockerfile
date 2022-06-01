FROM ubuntu:22.04

COPY nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter

EXPOSE 9835
ENTRYPOINT ["/usr/local/bin/nvidia_gpu_exporter"]
