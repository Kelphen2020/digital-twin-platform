// Command server boots the P0 digital-twin API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/bus"
	"digital-twin-platform/api/internal/config"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, closeStore, err := openStore(ctx, cfg)
	if err != nil {
		log.Fatalf("api: open store: %v", err)
	}
	defer closeStore()

	engine := rules.New(cfg.DedupeWindow)
	hub := realtime.NewHubWithBuffer(cfg.HubClientBuffer)
	meters := metrics.New()
	siteConfig, err := loadSite(cfg)
	if err != nil {
		log.Fatalf("api: load site config: %v", err)
	}

	authService := auth.NewWithRepo(cfg.AuthMode, cfg.JWTSecret, database)
	if err := authService.EnsureSeed(ctx); err != nil {
		log.Fatalf("api: seed users: %v", err)
	}
	server := api.New(database, engine, hub, meters, cfg.Version, authService, cfg.IngestToken)

	eventBus, err := openBus(cfg)
	if err != nil {
		log.Fatalf("api: open event bus: %v", err)
	}
	if eventBus != nil {
		server.AttachBus(eventBus)
		log.Printf("api: event bus driver=%s", cfg.BusDriver)
	}
	server.SetSite(siteConfig)
	server.SetTrackSample(cfg.WSTrackSample)
	server.SetIngestGuard(api.NewRateLimiter(cfg.IngestRateLimit, cfg.IngestBurst), cfg.MaxBodyBytes)
	server.SetLoginGuard(cfg.LoginMaxFailures, time.Duration(cfg.LoginLockoutSeconds)*time.Second)
	authService.SetAccessTTL(time.Duration(cfg.AccessTokenTTLSeconds) * time.Second)
	server.SetSessionTTL(time.Duration(cfg.RefreshTokenTTLSeconds) * time.Second)
	server.SetPatrolTimeouts(
		time.Duration(cfg.PatrolPendingTimeoutMinutes)*time.Minute,
		time.Duration(cfg.PatrolRunningTimeoutMinutes)*time.Minute,
	)

	server.LoadSettings(ctx)
	go server.RunStats(ctx, 5*time.Second)
	go server.RunEscalation(ctx, 15*time.Second)
	go server.RunPatrolScheduler(ctx, time.Duration(cfg.PatrolTickSeconds)*time.Second)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("api: listening on :%s (store=%s, version=%s)", cfg.Port, storeMode(cfg.DatabaseURL), cfg.Version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("api: serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("api: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("api: shutdown: %v", err)
	}
}

func openStore(ctx context.Context, cfg config.Config) (store.Store, func(), error) {
	if cfg.DatabaseURL != "" {
		postgres, err := store.NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, nil, err
		}
		log.Printf("api: PostgreSQL store ready")
		return postgres, postgres.Close, nil
	}
	memory := store.NewMemory(cfg.MaxEvents)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	log.Printf("api: in-memory store ready (6 demo cameras)")
	return memory, func() {}, nil
}

func loadSite(cfg config.Config) (model.Site, error) {
	site := model.DefaultSite()
	if cfg.SiteConfigFile == "" {
		return site, nil
	}
	data, err := os.ReadFile(cfg.SiteConfigFile)
	if err != nil {
		return site, err
	}
	if err := json.Unmarshal(data, &site); err != nil {
		return site, err
	}
	if site.Name == "" {
		site.Name = model.DefaultSite().Name
	}
	log.Printf("api: site config loaded from %s", cfg.SiteConfigFile)
	return site, nil
}

func openBus(cfg config.Config) (*bus.Bus, error) {
	switch cfg.BusDriver {
	case "jsonl":
		return bus.NewJSONL(cfg.BusDir)
	case "memory", "":
		return bus.NewMemory(cfg.BusBuffer), nil
	default:
		return nil, fmt.Errorf("unknown BUS_DRIVER %q", cfg.BusDriver)
	}
}

func storeMode(databaseURL string) string {
	if databaseURL == "" {
		return "memory"
	}
	return "postgres"
}
