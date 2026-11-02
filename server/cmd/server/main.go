// Command server runs the warble HTTP (OTA) + WS endpoints.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rebelthor/warble/server/internal/config"
	"github.com/rebelthor/warble/server/internal/configloader"
	"github.com/rebelthor/warble/server/internal/debug"
	"github.com/rebelthor/warble/server/internal/ota"
	"github.com/rebelthor/warble/server/internal/pipeline"
	"github.com/rebelthor/warble/server/internal/ws"
)

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("WARBLE_DEBUG") == "1" {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	cfg := config.Default()
	log.Info("starting",
		"host", cfg.Host,
		"ota_port", cfg.OTAPort,
		"ws_port", cfg.WSPort,
		"ws_path", cfg.WSPath,
		"local_ip", config.LocalIP(),
		"config_dir", cfg.ConfigDir,
	)

	loader, err := configloader.New(cfg.ConfigDir, log)
	if err != nil {
		log.Error("config.load", "err", err)
		os.Exit(1)
	}

	pipe := pipeline.New(pipeline.Config{
		WhisperURL:   cfg.WhisperURL,
		OllamaURL:    cfg.OllamaURL,
		OllamaAPIKey: cfg.OllamaAPIKey,
		PiperURL:     cfg.PiperURL,
		Loader:       loader,
	}, log)

	// Re-warm on prompt or model change so the next turn after a config
	// edit doesn't pay the prefix-cache miss (~2 s) or a full model swap.
	// Voice changes don't need this (Piper handles the per-voice load).
	loader.OnChange(func(old, new *configloader.Snapshot) {
		if old.SystemPrompt == new.SystemPrompt && old.OllamaModel == new.OllamaModel {
			return
		}
		log.Info("config.rewarm", "reason", "prompt-or-model-changed")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			pipe.Warmup(ctx)
		}()
	})

	stopPolling := make(chan struct{})
	defer close(stopPolling)
	loader.Start(stopPolling, 1*time.Second)

	// Pre-warm in the background so the OTA/WS ports come up immediately
	// for the device while the LLM model loads.
	warmupCtx, warmupCancel := context.WithTimeout(context.Background(), 120*time.Second)
	go func() {
		defer warmupCancel()
		pipe.Warmup(warmupCtx)
	}()

	otaMux := http.NewServeMux()
	ota.New(cfg, log).Register(otaMux)

	wsMux := http.NewServeMux()
	wsSrv := ws.New(cfg, pipe, log)
	wsSrv.Register(wsMux)

	// /debug/* endpoints are opt-in. Mounted on the OTA mux so they share
	// the existing HTTP listener (:8003); off by default to avoid letting
	// anything on the LAN drive the device's speaker without intent.
	if os.Getenv("WARBLE_DEBUG") == "1" {
		debug.New(wsSrv, log).Register(otaMux)
		log.Info("debug.enabled", "endpoints", []string{"/debug/sessions", "/debug/say"})
	}

	otaServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.OTAPort),
		Handler:           otaMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	wsServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.WSPort),
		Handler: wsMux,
		// No ReadHeaderTimeout on the WS server because the coder/websocket
		// library hijacks the connection for arbitrarily long sessions.
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go runServer(ctx, &wg, log, "ota", otaServer)
	go runServer(ctx, &wg, log, "ws", wsServer)
	wg.Wait()
	log.Info("stopped")
}

func runServer(ctx context.Context, wg *sync.WaitGroup, log *slog.Logger, name string, srv *http.Server) {
	defer wg.Done()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("listening", "service", name, "addr", srv.Addr)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "service", name, "err", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "service", name, "err", err)
		}
	}
}
