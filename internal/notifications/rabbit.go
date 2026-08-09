package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

const Queue = "digital-notary.notifications"

type Message struct{ EventID, DocumentID, Recipient, Kind string }
type Publisher struct {
	connection *amqp091.Connection
	channel    *amqp091.Channel
}

func Connect(url string) (*Publisher, error) {
	c, err := amqp091.Dial(url)
	if err != nil {
		return nil, err
	}
	ch, err := c.Channel()
	if err != nil {
		c.Close()
		return nil, err
	}
	if _, err = ch.QueueDeclare(Queue, true, false, false, false, nil); err != nil {
		ch.Close()
		c.Close()
		return nil, err
	}
	return &Publisher{c, ch}, nil
}
func (p *Publisher) Publish(ctx context.Context, m Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return p.channel.PublishWithContext(ctx, "", Queue, false, false, amqp091.Publishing{ContentType: "application/json", DeliveryMode: amqp091.Persistent, MessageId: m.EventID, Timestamp: time.Now().UTC(), Body: raw})
}
func (p *Publisher) Close() {
	if p.channel != nil {
		p.channel.Close()
	}
	if p.connection != nil {
		p.connection.Close()
	}
}

type Handler func(context.Context, Message) error

func Consume(url string, handler Handler) error {
	p, err := Connect(url)
	if err != nil {
		return err
	}
	defer p.Close()
	deliveries, err := p.channel.Consume(Queue, "notification-worker", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for d := range deliveries {
		var m Message
		if err := json.Unmarshal(d.Body, &m); err == nil {
			err = handler(context.Background(), m)
		}
		if err != nil {
			d.Nack(false, true)
			continue
		}
		if err := d.Ack(false); err != nil {
			return fmt.Errorf("ack notification: %w", err)
		}
	}
	return nil
}

type Sender interface {
	Send(context.Context, Message) error
}
type HTTPGatewaySender struct {
	url, token string
	client     *http.Client
}

func NewHTTPGatewaySender(url, token string) *HTTPGatewaySender {
	return &HTTPGatewaySender{url: url, token: token, client: &http.Client{Timeout: 10 * time.Second}}
}
func (s *HTTPGatewaySender) Send(ctx context.Context, m Message) error {
	raw, err := json.Marshal(map[string]string{"to": m.Recipient, "text": "Статус документа изменён. Документ: " + m.DocumentID, "eventId": m.EventID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("SMS gateway returned %s", res.Status)
	}
	return nil
}

type LogSender struct{}

func (LogSender) Send(_ context.Context, m Message) error {
	fmt.Printf("notification event=%s document=%s recipient=%s kind=%s\n", m.EventID, m.DocumentID, m.Recipient, m.Kind)
	return nil
}
