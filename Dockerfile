FROM alpine:3.12

COPY nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter

# Temporarily disabled, since "apk add" is broken on buildx atm
#RUN apk add --no-cache tini
#ENTRYPOINT ["/sbin/tini", "--"]

CMD ["/usr/local/bin/nvidia_gpu_exporter"]
