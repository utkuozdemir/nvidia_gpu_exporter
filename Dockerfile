FROM alpine:3.12

COPY nvidia-gpu-exporter /usr/local/bin/nvidia-gpu-exporter

# Temporarily disabled, since "apk add" is broken on buildx atm
#RUN apk add --no-cache tini
#ENTRYPOINT ["/sbin/tini", "--"]

CMD ["/usr/local/bin/nvidia-gpu-exporter"]
