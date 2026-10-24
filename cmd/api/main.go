package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trackfund/internal/composition"
	"trackfund/internal/config"
	httpapi "trackfund/internal/interfaces/http"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	app, err := composition.NewApp(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Closer()

	// use slog to log startup/shutdown and capture from app.Logger if present
	var lg *slog.Logger
	if app.Logger != nil {
		lg = app.Logger.WithContext(ctx)
	} else {
		lg = slog.Default()
	}

	server := httpapi.NewServer(app)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: server.Handler(),
	}

	// start server in background
	go func() {
		lg.Info("starting server", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			lg.Error("server failed", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	lg.Info("shutdown signal received")

	ctxShut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctxShut); err != nil {
		lg.Error("graceful shutdown failed", slog.Any("err", err))
	} else {
		lg.Info("server stopped")
	}
}
