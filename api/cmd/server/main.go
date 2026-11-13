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

	firebase "firebase.google.com/go/v4"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/option"

	"github.com/mandamas/api/internal/allowlist"
	"github.com/mandamas/api/internal/config"
	"github.com/mandamas/api/internal/httpapi"
	"github.com/mandamas/api/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var opts []option.ClientOption
	if cfg.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(cfg.CredentialsFile))
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: cfg.ProjectID}, opts...)
	if err != nil {
		return err
	}
	authClient, err := app.Auth(ctx)
	if err != nil {
		return err
	}
	fs, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	defer fs.Close()

	var list allowlist.Checker
	if cfg.AuthEmulator {
		slog.Warn("auth emulator mode: allowlist from DEV_ALLOWED_EMAILS, not Remote Config")
		list = allowlist.NewStatic(cfg.DevAllowedEmail)
	} else {
		cached := allowlist.NewCached(allowlist.RemoteConfigFetcher(cfg.ProjectID, cfg.AllowlistParam), cfg.AllowlistTTL)
		if _, err := cached.Allowed(ctx, ""); err != nil {
			slog.Error("allowlist not loaded yet; everyone is denied until it loads", "err", err)
		}
		list = cached
	}

	router := httpapi.NewRouter(httpapi.Options{
		Verifier: authClient, Allowlist: list, Store: store.NewFirestore(fs),
		AppDir: cfg.AppDir, TrustedProxies: cfg.TrustedProxies,
	})
	srv := &http.Server{
		Addr: ":" + cfg.Port, Handler: router,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("listening", "port", cfg.Port, "app", cfg.AppDir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
