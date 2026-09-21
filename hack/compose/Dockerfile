# Dev image for the compose stack. Builds the exporter and the fake nvidia-smi
# from source, so the whole stack runs on a machine with no GPU: the exporter
# scrapes the fake, which replays an embedded capture. Not a release artifact
# (goreleaser builds those); this exists only for hack/compose.
FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/nvidia_gpu_exporter ./cmd/nvidia_gpu_exporter \
 && CGO_ENABLED=0 go build -o /out/fake-nvidia-smi ./cmd/fake-nvidia-smi

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
COPY --from=build /out/nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter
COPY --from=build /out/fake-nvidia-smi /usr/local/bin/fake-nvidia-smi
EXPOSE 9835
ENTRYPOINT ["/usr/local/bin/nvidia_gpu_exporter"]
