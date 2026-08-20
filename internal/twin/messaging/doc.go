// Package messaging extracts message broker topologies for the Digital Twin's
// Messaging category. It scans for Kafka, RabbitMQ, Redis Pub/Sub, and NATS
// usage, producing domain.Topic nodes with publishes_to/subscribes_to edges.
// Deterministic — no LLM.
package messaging
