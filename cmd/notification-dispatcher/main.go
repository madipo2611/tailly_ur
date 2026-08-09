package main

import (
	"context"
	"log"
	"os"
	"strings"

	"digital-notary/internal/notifications"
	"digital-notary/internal/persistence"
	"github.com/segmentio/kafka-go"
)

func main() {
	dsn, brokers, rabbit := os.Getenv("DATABASE_URL"), os.Getenv("KAFKA_BROKERS"), os.Getenv("RABBITMQ_URL")
	if dsn == "" || brokers == "" || rabbit == "" {
		log.Fatal("DATABASE_URL, KAFKA_BROKERS and RABBITMQ_URL are required")
	}
	pool, err := persistence.Open(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	publisher, err := notifications.Connect(rabbit)
	if err != nil {
		log.Fatal(err)
	}
	defer publisher.Close()
	reader := kafka.NewReader(kafka.ReaderConfig{Brokers: strings.Split(brokers, ","), Topic: env("KAFKA_TOPIC", "digital-notary.documents"), GroupID: "notification-dispatcher", MinBytes: 1, MaxBytes: 10e6})
	defer reader.Close()
	repo := persistence.NewRepository(pool)
	for {
		message, err := reader.FetchMessage(context.Background())
		if err != nil {
			log.Printf("kafka fetch: %v", err)
			continue
		}
		eventID := header(message.Headers, "event_id")
		customer, contractor, err := repo.NotificationTargets(context.Background(), string(message.Key))
		if err == nil {
			for _, recipient := range []string{customer, contractor} {
				if err = publisher.Publish(context.Background(), notifications.Message{EventID: eventID, DocumentID: string(message.Key), Recipient: recipient, Kind: "document.status_changed"}); err != nil {
					break
				}
			}
		}
		if err != nil {
			log.Printf("dispatch event %s: %v", eventID, err)
			continue
		}
		if err = reader.CommitMessages(context.Background(), message); err != nil {
			log.Printf("commit event: %v", err)
		}
	}
}
func header(headers []kafka.Header, key string) string {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
