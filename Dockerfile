# minimal glibc base: the NVIDIA container runtime injects nvidia-smi from the
# host, and that binary needs a libc and its loader inside the container, so
# scratch/static bases cannot work here
FROM gcr.io/distroless/base-nossl-debian13:latest@sha256:de74d0660f05c00818d6cb18792e0ef4f7de2e94556a5978030369d0794d5232

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nvidia_gpu_exporter /usr/bin/nvidia_gpu_exporter

# stays root (the base default) on purpose: changing the execution identity
# of a widely deployed image is a migration of its own. GPU access itself
# does not need root, and the chart documents a nonroot security context

EXPOSE 9835
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
