# Dev image for the compose stack. Builds the exporter and the fake nvidia-smi
# from source, so the whole stack runs on a machine with no GPU: the exporter
# scrapes the fake, which replays an embedded capture. Not a release artifact
# (goreleaser builds those); this exists only for hack/compose.
FROM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/nvidia_gpu_exporter ./cmd/nvidia_gpu_exporter \
 && CGO_ENABLED=0 go build -o /out/fake-nvidia-smi ./cmd/fake-nvidia-smi

FROM alpine:3.24@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a
COPY --from=build /out/nvidia_gpu_exporter /usr/local/bin/nvidia_gpu_exporter
COPY --from=build /out/fake-nvidia-smi /usr/local/bin/fake-nvidia-smi
EXPOSE 9835
ENTRYPOINT ["/usr/local/bin/nvidia_gpu_exporter"]
