# minimal glibc base: the cgo binary and the injected driver library (or
# nvidia-smi when running with --collect.backend=exec) need a libc and its
# loader inside the container, so scratch/static bases cannot work here
FROM gcr.io/distroless/base-nossl-debian13:latest@sha256:f21fdfe4ede467d9ea8a6caaec1f8381ea4502243a3e8bb1c43df80c6fbaaeae

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

# make the NVIDIA container runtime inject the driver library (libnvidia-ml)
# even when the pod/container spec sets no NVIDIA_* env itself
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=utility

# nothing the exporter does needs root. Keep the uid numeric: the kubelet
# cannot verify runAsNonRoot against a named image user and refuses to start
# the container, so `USER nobody` would break every pod that asks for one
USER 65534:65534

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
