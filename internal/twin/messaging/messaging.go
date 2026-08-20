package messaging

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

// Extractor scans a source tree for message broker usage and emits
// domain.Topic nodes with producer/consumer edges.
type Extractor struct {
	root string
}

// New returns a messaging extractor rooted at root.
func New(root string) *Extractor {
	return &Extractor{root: root}
}

// Extract walks root, scans each source file for broker patterns, and
// returns the discovered topic nodes and producer/consumer edges.
func (e *Extractor) Extract() ([]domain.Node, []domain.Edge, error) {
	var nodes []domain.Node
	var edges []domain.Edge
	// seenTopics dedupes topic nodes across the whole tree so the same topic
	// referenced in multiple files collapses into a single node.
	seenTopics := map[string]bool{}
	err := filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if !isSourceExt(ext) {
			return nil
		}
		n, ed := e.extractFile(path, seenTopics)
		nodes, edges = append(nodes, n...), append(edges, ed...)
		return nil
	})
	return nodes, edges, err
}

// pattern defines a broker-specific topic/queue extraction pattern.
type pattern struct {
	broker   string
	regex    *regexp.Regexp
	kind     string         // "publishes_to" or "subscribes_to"
	groupIdx int            // which regex group holds the topic name
	multi    *regexp.Regexp // when set, groupIdx captures a list and multi finds each topic within it
}

var patterns = []pattern{
	// Kafka (Go sarama): producer.SendMessage with Topic, or ConsumerGroup with Topics
	{broker: "kafka", regex: regexp.MustCompile(`Topic:\s*"([^"]+)"`), kind: "publishes_to", groupIdx: 1},
	{broker: "kafka", regex: regexp.MustCompile(`Topics:\s*\[\]string\{(.*?)\}`), kind: "subscribes_to", groupIdx: 1, multi: regexp.MustCompile(`"([^"]+)"`)},
	// Kafka (Python): KafkaProducer(topic="..."), KafkaConsumer("...")
	{broker: "kafka", regex: regexp.MustCompile(`KafkaProducer\(.*?topic\s*=\s*['"]([^'"]+)['"]`), kind: "publishes_to", groupIdx: 1},
	{broker: "kafka", regex: regexp.MustCompile(`KafkaConsumer\(\s*['"]([^'"]+)['"]`), kind: "subscribes_to", groupIdx: 1},
	// RabbitMQ (Go amqp): channel.Publish(exchange, "queue", ...), channel.Consume("queue", ...)
	{broker: "rabbitmq", regex: regexp.MustCompile(`\.Publish\([^,]+,\s*"([^"]+)"`), kind: "publishes_to", groupIdx: 1},
	{broker: "rabbitmq", regex: regexp.MustCompile(`\.Consume\(\s*"([^"]+)"`), kind: "subscribes_to", groupIdx: 1},
	// RabbitMQ (Python pika): queue_declare(queue="...")
	{broker: "rabbitmq", regex: regexp.MustCompile(`queue_declare\(queue\s*=\s*['"]([^'"]+)['"]`), kind: "publishes_to", groupIdx: 1},
	// Redis Pub/Sub: .Publish("channel", ...), .Subscribe("channel")
	{broker: "redis", regex: regexp.MustCompile(`\.Publish\(\s*"([^"]+)"`), kind: "publishes_to", groupIdx: 1},
	{broker: "redis", regex: regexp.MustCompile(`\.Subscribe\(\s*"([^"]+)"`), kind: "subscribes_to", groupIdx: 1},
	// NATS: nats.Publish("subject", ...), nats.Subscribe("subject", ...)
	{broker: "nats", regex: regexp.MustCompile(`nats\.Publish\(\s*"([^"]+)"`), kind: "publishes_to", groupIdx: 1},
	{broker: "nats", regex: regexp.MustCompile(`nats\.Subscribe\(\s*"([^"]+)"`), kind: "subscribes_to", groupIdx: 1},
}

func (e *Extractor) extractFile(path string, seenTopics map[string]bool) ([]domain.Node, []domain.Edge) {
	relPath, _ := filepath.Rel(e.root, path)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var nodes []domain.Node
	var edges []domain.Edge

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		for _, p := range patterns {
			matches := p.regex.FindStringSubmatch(line)
			if matches == nil || len(matches) <= p.groupIdx {
				continue
			}
			// Collect topic names from this match: a single name, or (when
			// p.multi is set) every name inside a captured slice/list.
			topicNames := []string{matches[p.groupIdx]}
			if p.multi != nil {
				topicNames = topicNames[:0]
				for _, m := range p.multi.FindAllStringSubmatch(matches[p.groupIdx], -1) {
					if len(m) > 1 {
						topicNames = append(topicNames, m[1])
					}
				}
			}
			for _, topicName := range topicNames {
				if topicName == "" {
					continue
				}
				topicID := "topic:" + ids.Escape(topicName)
				if !seenTopics[topicID] {
					seenTopics[topicID] = true
					nodes = append(nodes, domain.Node{
						ID:    topicID,
						Kind:  "topic",
						Label: topicName,
						Topic: &domain.Topic{Name: topicName, Broker: p.broker},
					})
				}
				// Edge from file to topic (producer or consumer relationship)
				edges = append(edges, domain.Edge{
					From: "file:" + relPath,
					To:   topicID,
					Kind: p.kind,
				})
			}
		}
	}
	return nodes, edges
}

func isIgnoreDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "out", "target",
		".venv", "venv", "__pycache__", ".cache", ".idea", ".vscode",
		".kern", "tmp", "coverage":
		return true
	}
	return false
}

func isSourceExt(ext string) bool {
	switch ext {
	case ".go", ".js", ".mjs", ".ts", ".py", ".java", ".rb", ".php":
		return true
	}
	return false
}
