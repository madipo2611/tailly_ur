package main

import (
	"context"
	"digital-notary/internal/events"
	"digital-notary/internal/persistence"
	"log"
	"os"
	"time"
)

func main() {
	dsn, brokers := os.Getenv("DATABASE_URL"), os.Getenv("KAFKA_BROKERS")
	if dsn == "" || brokers == "" {
		log.Fatal("DATABASE_URL and KAFKA_BROKERS are required")
	}
	pool, err := persistence.Open(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	publisher := events.NewKafkaPublisher(brokers, env("KAFKA_TOPIC", "digital-notary.documents"))
	defer publisher.Close()
	for {
		n, err := persistence.NewRepository(pool).PublishPending(context.Background(), publisher, 50)
		if err != nil {
			log.Printf("outbox publish: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if n == 0 {
			time.Sleep(time.Second)
		}
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
