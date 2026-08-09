package events

import (
	"context"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

type Message struct {
	ID, AggregateID, Type string
	Payload               []byte
}
type Publisher interface {
	Publish(context.Context, Message) error
	Close() error
}
type KafkaPublisher struct{ writer *kafka.Writer }

func NewKafkaPublisher(brokers, topic string) *KafkaPublisher {
	return &KafkaPublisher{writer: &kafka.Writer{Addr: kafka.TCP(strings.Split(brokers, ",")...), Topic: topic, Balancer: &kafka.Hash{}, RequiredAcks: kafka.RequireAll, WriteTimeout: 10 * time.Second}}
}
func (p *KafkaPublisher) Publish(ctx context.Context, m Message) error {
	return p.writer.WriteMessages(ctx, kafka.Message{Key: []byte(m.AggregateID), Value: m.Payload, Headers: []kafka.Header{{Key: "event_id", Value: []byte(m.ID)}, {Key: "event_type", Value: []byte(m.Type)}}})
}
func (p *KafkaPublisher) Close() error { return p.writer.Close() }
