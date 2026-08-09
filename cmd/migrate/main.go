package main

import (
	"context"
	"log"
	"os"

	"digital-notary/internal/persistence"
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
	if err = persistence.Migrate(context.Background(), pool); err != nil {
		log.Fatal(err)
	}
	log.Print("database migrations applied")
}
