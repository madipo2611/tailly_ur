package main

import (
	"context"
	"digital-notary/internal/persistence"
	"log"
	"os"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	pool, err := persistence.Open(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = persistence.NewRepository(pool).CleanupExpired(context.Background()); err != nil {
		log.Fatal(err)
	}
	log.Print("expired security records removed")
}
