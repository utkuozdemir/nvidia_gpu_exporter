# Dev image for the compose stack. Builds the exporter and the fake nvidia-smi
# from source, so the whole stack runs on a machine with no GPU: the exporter
# scrapes the fake, which replays an embedded capture. Not a release artifact
# (goreleaser builds those); this exists only for hack/compose.
FROM golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/nvidia_gpu_exporter ./cmd/nvidia_gpu_exporter \
 && CGO_ENABLED=0 go build -o /out/fake-nvidia-smi ./cmd/fake-nvidia-smi

FROM alpine:3.24@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35
COPY --from=build /out/nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter
COPY --from=build /out/fake-nvidia-smi /usr/local/bin/fake-nvidia-smi
EXPOSE 9835
ENTRYPOINT ["/usr/local/bin/nvidia_gpu_exporter"]
