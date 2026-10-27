# Dev image for the compose stack. Builds the exporter and the fake nvidia-smi
# from source, so the whole stack runs on a machine with no GPU: the exporter
# scrapes the fake, which replays an embedded capture. Not a release artifact
# (goreleaser builds those); this exists only for hack/compose.
FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/nvidia_gpu_exporter ./cmd/nvidia_gpu_exporter \
 && CGO_ENABLED=0 go build -o /out/fake-nvidia-smi ./cmd/fake-nvidia-smi

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
COPY --from=build /out/nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter
COPY --from=build /out/fake-nvidia-smi /usr/local/bin/fake-nvidia-smi
EXPOSE 9835
ENTRYPOINT ["/usr/local/bin/nvidia_gpu_exporter"]
