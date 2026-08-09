package main

import (
	"context"
	"digital-notary/internal/notifications"
	"log"
	"os"
)

func main() {
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		log.Fatal("RABBITMQ_URL is required")
	}
	var sender notifications.Sender = notifications.LogSender{}
	if gateway := os.Getenv("SMS_GATEWAY_URL"); gateway != "" {
		sender = notifications.NewHTTPGatewaySender(gateway, os.Getenv("SMS_GATEWAY_TOKEN"))
	}
	log.Fatal(notifications.Consume(url, func(ctx context.Context, m notifications.Message) error { return sender.Send(ctx, m) }))
}
