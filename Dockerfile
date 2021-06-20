FROM ubuntu:20.04

RUN apt-get update && \
    apt-get install -y tini && \
    rm -rf /var/lib/apt/lists/*

COPY nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/nvidia_gpu_exporter"]
