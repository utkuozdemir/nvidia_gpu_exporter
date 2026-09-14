# minimal glibc base: the NVIDIA container runtime injects nvidia-smi from the
# host, and that binary needs a libc and its loader inside the container, so
# scratch/static bases cannot work here
FROM gcr.io/distroless/base-nossl-debian13:latest@sha256:af5cb8dd589b8520b8c06bebb9efb73d7e16406cab58e85c51761fff49d370a0

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

# nothing the exporter does needs root. Keep the uid numeric: the kubelet
# cannot verify runAsNonRoot against a named image user and refuses to start
# the container, so `USER nobody` would break every pod that asks for one
USER 65534:65534

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
