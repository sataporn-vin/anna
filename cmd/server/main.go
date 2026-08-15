package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"anna/internal/config"
	"anna/internal/httpapi"
	"anna/internal/memory"
	"anna/internal/mongostore"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()
	store, err := mongostore.Connect(startupCtx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := store.Close(closeCtx); err != nil {
			logger.Error("database close failed", "error", err)
		}
	}()

	application := memory.NewService(store, memory.Limits{
		MaxCollections:    cfg.MaxCollections,
		MaxResultRecords:  cfg.MaxResultRecords,
		MaxResultBytes:    cfg.MaxResultBytes,
		MaxPipelineStages: cfg.MaxPipelineStages,
		OperationTimeout:  cfg.MongoOperationTimeout,
	}, cfg.DefaultTimezone)
	if err := application.EnsureBootstrap(startupCtx); err != nil {
		logger.Error("database bootstrap failed", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(application, cfg.AuthBearerToken, cfg.MaxRequestBytes, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server starting", "address", cfg.HTTPAddr)
		serverErrors <- httpServer.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case signal := <-signals:
		logger.Info("shutdown requested", "signal", signal.String())
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}
