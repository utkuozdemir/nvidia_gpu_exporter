//go:build !linux

package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/prometheus/exporter-toolkit/web"
)

func listenAndServe(
	ctx context.Context,
	server *http.Server,
	flags *web.FlagConfig,
	network string,
	logger *slog.Logger,
) (retErr error) {
	return listenTCP(ctx, server, flags, network, logger)
}
