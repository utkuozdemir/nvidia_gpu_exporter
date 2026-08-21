# minimal glibc base: the NVIDIA container runtime injects nvidia-smi from the
# host, and that binary needs a libc and its loader inside the container, so
# scratch/static bases cannot work here
FROM gcr.io/distroless/base-nossl-debian13:latest@sha256:e50761cbc75cbd24ed76553350f67c44dda9d4a9b9c9e8f44bed6ddeb3cb8a9a

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

# stays root (the base default) on purpose: changing the execution identity
# of a widely deployed image is a migration of its own. GPU access itself
# does not need root, and the chart documents a nonroot security context

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
