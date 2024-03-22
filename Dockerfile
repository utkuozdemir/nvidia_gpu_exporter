FROM nvidia/cuda:11.6.2-base-ubuntu20.04

COPY nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
