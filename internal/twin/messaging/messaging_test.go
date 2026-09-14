package messaging

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractKafkaTopics(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "producer.go"), []byte(`
package main
msg := &sarama.ProducerMessage{Topic: "orders", Value: sarama.ByteEncoder(data)}
`))
	writeFile(t, filepath.Join(dir, "consumer.go"), []byte(`
package main
consumer := &sarama.ConsumerGroup{Topics: []string{"orders", "payments"}}
`))

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 topics", len(nodes))
	}
	hasPub, hasSub := false, false
	for _, e := range edges {
		if e.Kind == "publishes_to" {
			hasPub = true
		}
		if e.Kind == "subscribes_to" {
			hasSub = true
		}
	}
	if !hasPub {
		t.Error("no publishes_to edges")
	}
	if !hasSub {
		t.Error("no subscribes_to edges")
	}
}

func TestExtractRabbitMQQueues(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "producer.go"), []byte(`
package main
ch.Publish("", "task_queue", false, false, amqp.Publishing{})
`))
	writeFile(t, filepath.Join(dir, "consumer.go"), []byte(`
package main
msgs, _ := ch.Consume("task_queue", "", true, false, false, false, nil)
`))
	writeFile(t, filepath.Join(dir, "pika.py"), []byte(`
import pika
channel.queue_declare(queue="task_queue")
`))

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1 queue", len(nodes))
	}
	if nodes[0].Topic == nil {
		t.Fatal("topic node missing Topic detail")
	}
	if nodes[0].Topic.Broker != "rabbitmq" {
		t.Fatalf("broker = %q, want rabbitmq", nodes[0].Topic.Broker)
	}
	hasPub, hasSub := false, false
	for _, e := range edges {
		if e.Kind == "publishes_to" {
			hasPub = true
		}
		if e.Kind == "subscribes_to" {
			hasSub = true
		}
	}
	if !hasPub {
		t.Error("no publishes_to edges")
	}
	if !hasSub {
		t.Error("no subscribes_to edges")
	}
}

func TestExtractRedisPubSub(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pub.go"), []byte(`
package main
client.Publish("notifications", payload)
`))
	writeFile(t, filepath.Join(dir, "sub.go"), []byte(`
package main
client.Subscribe("notifications")
`))

	e := New(dir)
	nodes, edges, err := e.Extract()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1 channel", len(nodes))
	}
	if nodes[0].Topic == nil || nodes[0].Topic.Broker != "redis" {
		t.Fatalf("unexpected topic detail: %+v", nodes[0].Topic)
	}
	hasPub, hasSub := false, false
	for _, e := range edges {
		if e.Kind == "publishes_to" {
			hasPub = true
		}
		if e.Kind == "subscribes_to" {
			hasSub = true
		}
	}
	if !hasPub {
		t.Error("no publishes_to edges")
	}
	if !hasSub {
		t.Error("no subscribes_to edges")
	}
}
