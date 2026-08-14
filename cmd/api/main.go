package main

import (
	"bufio"
	"context"
	"digital-notary/internal/auth"
	"digital-notary/internal/httpapi"
	"digital-notary/internal/persistence"
	"digital-notary/internal/service"
	"digital-notary/internal/storage"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed web/*
var webFS embed.FS

func main() {
	if path := os.Getenv("VAULT_ENV_FILE"); path != "" {
		if err := loadEnvFile(path); err != nil {
			log.Fatal(err)
		}
	}
	objects := storage.ObjectStore(storage.NewMemoryStore())
	if bucket := os.Getenv("S3_BUCKET"); bucket != "" {
		s3Store, err := storage.NewS3Store(os.Getenv("S3_ENDPOINT"), os.Getenv("S3_REGION"), bucket, os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY"))
		if err != nil {
			log.Fatal(err)
		}
		objects = s3Store
	} else if remoteURL := os.Getenv("OBJECT_STORAGE_URL"); remoteURL != "" {
		var client *http.Client
		cert, key := os.Getenv("STORAGE_CLIENT_CERT"), os.Getenv("STORAGE_CLIENT_KEY")
		if cert != "" || key != "" {
			if cert == "" || key == "" {
				log.Fatal("both STORAGE_CLIENT_CERT and STORAGE_CLIENT_KEY are required")
			}
			var err error
			client, err = storage.NewMTLSClient(cert, key, os.Getenv("STORAGE_CA_CERT"))
			if err != nil {
				log.Fatal(err)
			}
		}
		remote, err := storage.NewHTTPStore(remoteURL, os.Getenv("OBJECT_STORAGE_TOKEN"), client)
		if err != nil {
			log.Fatal(err)
		}
		objects = remote
	}
	var state *persistence.Repository
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pool, err := persistence.Open(context.Background(), dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
		state = persistence.NewRepository(pool)
	}
	app := service.NewAppWithPersistence(os.Getenv("SIGNING_URL_BASE"), "", objects, state)
	sber, err := auth.NewSberFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	api := httpapi.New(app, sber)
	allowedOrigins := map[string]bool{}
	for _, origin := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowedOrigins[origin] = true
		}
	}
	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	ui := http.FileServer(http.FS(assets))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if origin := r.Header.Get("Origin"); allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; base-uri 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/sign/") {
			r.URL.Path = "/"
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/app.css" || r.URL.Path == "/app.js" {
			ui.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}
	server := &http.Server{Addr: ":" + addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		log.Printf("digital-notary api listening on :%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return s.Err()
}
