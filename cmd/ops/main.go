package main

import (
	"communication-infrastructure/internal/ops"
	"communication-infrastructure/internal/provider"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func localURL(raw string, postgres bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return errors.New("demo dependencies must use a loopback address")
	}
	if postgres && u.Path != "/covent_ops_demo" && u.Path != "/covent_ops_test" {
		return errors.New("the database must be covent_ops_demo or covent_ops_test")
	}
	return nil
}
func run() error {
	if env("DEMO_MODE", "false") != "true" {
		return errors.New("set DEMO_MODE=true; this application implements only synthetic providers")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	dbURL := env("DATABASE_URL", "postgres://ops_app:ops_demo_app@127.0.0.1:55442/covent_ops_demo?sslmode=disable")
	adminURL := env("MIGRATION_DATABASE_URL", "postgres://ops_admin:ops_demo_admin@127.0.0.1:55442/covent_ops_demo?sslmode=disable")
	redisURL := env("REDIS_URL", "redis://127.0.0.1:56389/0")
	for _, raw := range []string{dbURL, adminURL} {
		if err := localURL(raw, true); err != nil {
			return err
		}
	}
	if err := localURL(redisURL, false); err != nil {
		return err
	}
	appParsed, _ := url.Parse(dbURL)
	adminParsed, _ := url.Parse(adminURL)
	if appParsed.Host != adminParsed.Host || appParsed.Path != adminParsed.Path {
		return errors.New("migration and runtime connections must target the same isolated database")
	}
	addr := env("LISTEN_ADDR", "127.0.0.1:2061")
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("the demo API must listen on a loopback address")
	}
	startup, cancelStartup := context.WithTimeout(ctx, 20*time.Second)
	defer cancelStartup()
	if err = ops.Migrate(startup, adminURL, "migrations"); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	db, err := pgxpool.New(startup, dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.Ping(startup); err != nil {
		return err
	}
	var unsafe bool
	if err = db.QueryRow(startup, "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return errors.New("runtime database user must not bypass row security")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return err
	}
	options.DialTimeout = time.Second
	options.ReadTimeout = time.Second
	options.WriteTimeout = time.Second
	options.MaxRetries = -1
	client := redis.NewClient(options)
	defer client.Close()
	store := &ops.Store{DB: db, Redis: client}
	if err = store.EnsureDemo(startup); err != nil {
		return err
	}
	if env("INFRA_LAB", "false") == "true" {
		lab, labErr := provider.NewLab("http://" + addr)
		if labErr != nil {
			return labErr
		}
		defer lab.Close()
		gateway, labErr := lab.Gateway()
		if labErr != nil {
			return labErr
		}
		store.Gateway = gateway
		store.ProviderLab = lab
		store.CallbackOrigin = lab.CallbackOrigin
		peer, peerErr := provider.NewSIPPeer()
		if peerErr != nil {
			return peerErr
		}
		defer peer.Close()
		store.SIPPeer = peer
		if err = store.EnsureInfrastructure(startup); err != nil {
			return err
		}
		if env("IMESSAGE_LAB", "false") == "true" {
			bridgeLab, bridgeErr := provider.NewIMessageLab()
			if bridgeErr != nil {
				return bridgeErr
			}
			defer bridgeLab.Close()
			store.IMessageBridge, bridgeErr = bridgeLab.Client()
			if bridgeErr != nil {
				return bridgeErr
			}
			store.IMessageLab = bridgeLab
			store.Gateway = provider.WithIMessage{Default: store.Gateway, Bridge: store.IMessageBridge}
			if err = store.EnsureIMessage(startup); err != nil {
				return err
			}
		}
	}
	if env("IMESSAGE_LAB", "false") == "true" && store.IMessageBridge == nil {
		return errors.New("IMESSAGE_LAB requires INFRA_LAB=true")
	}
	store.DNSTargets = map[string]string{}
	for _, entry := range strings.Split(env("EMAIL_DIAGNOSTICS_TARGETS", ""), ",") {
		if entry == "" {
			continue
		}
		asset, domain, ok := strings.Cut(entry, "=")
		if !ok || (asset != "email-north" && asset != "email-east") || !provider.ValidDomain(domain) {
			return errors.New("EMAIL_DIAGNOSTICS_TARGETS must map email-north or email-east to a valid owned domain")
		}
		store.DNSTargets[asset] = domain
	}
	if err = store.EnsureCampaigns(startup); err != nil {
		return err
	}
	go store.RunWorker(ctx)
	app := &ops.Server{Store: store, Origin: env("ALLOWED_ORIGIN", "http://localhost:5173"), WebDir: env("WEB_DIR", "web/dist")}
	server := &http.Server{Addr: addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Covent Ops synthetic demo at http://%s", addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func main() {
	if err := run(); err != nil { // Connection URLs are never logged, including credentials.
		message := err.Error()
		if strings.Contains(message, "postgres://") || strings.Contains(message, "redis://") {
			message = "startup dependency configuration failed"
		}
		log.Fatal(message)
	}
}
