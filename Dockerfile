FROM ubuntu:20.04

COPY nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter

CMD ["/usr/local/bin/nvidia_gpu_exporter"]
