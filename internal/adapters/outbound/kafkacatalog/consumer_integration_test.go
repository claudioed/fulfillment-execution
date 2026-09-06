//go:build integration

package kafkacatalog

import (
	"context"
	"os"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// TestNewConsumer_TwoInstancesInARow_BothReplayFully is the real
// regression test for the "shared consumer group" bug this package
// shipped with: a second NewConsumer call (simulating a service
// restart) must independently replay Topic's full history into its own
// cache, NOT resume from wherever the first instance's consumer group
// left off.
//
// Requires KAFKA_BROKERS to point at a real, reachable broker (e.g.
// KAFKA_BROKERS=localhost:9092) -- this fleet's own CI `integration`
// job only provisions a Postgres service container, no Kafka, so this
// test SKIPS (not fails) when KAFKA_BROKERS is unset, mirroring
// labor-performance's identical
// internal/adapters/inbound/kafka/consumer_integration_test.go
// convention. Run locally with:
//
//	KAFKA_BROKERS=localhost:9092 go test -tags=integration ./internal/adapters/outbound/kafkacatalog/...
func TestNewConsumer_TwoInstancesInARow_BothReplayFully(t *testing.T) {
	brokersCSV := os.Getenv("KAFKA_BROKERS")
	if brokersCSV == "" {
		t.Skip("KAFKA_BROKERS not set; skipping kafka integration test")
	}
	brokers := []string{brokersCSV}
	topic := "warehouse.process-path-management.events.itest2"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	writer := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, AllowAutoTopicCreation: true}
	var publishErr error
	for attempt := 0; attempt < 5; attempt++ {
		publishErr = writer.WriteMessages(ctx, kafkago.Message{
			Value: []byte(`{"event_type":"ProcessPathCreated","data":{"path_id":"ITEST2","match_prefix":"itest2","direct":true,"required_capabilities":["itest2"]}}`),
		})
		if publishErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if publishErr != nil {
		t.Fatalf("seed publish: %v", publishErr)
	}
	_ = writer.Close()

	target, err := newTargetOffsets(ctx, brokers, topic)
	if err != nil {
		t.Fatalf("newTargetOffsets: %v", err)
	}
	if len(target) == 0 {
		t.Fatal("expected at least one partition with a message")
	}

	// First instance: real reader under a real unique group, consumes
	// the message and commits.
	group1 := uniqueConsumerGroup()
	reader1 := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: group1, StartOffset: kafkago.FirstOffset})
	if _, err := reader1.ReadMessage(ctx); err != nil {
		t.Fatalf("first reader ReadMessage: %v", err)
	}
	if err := reader1.CommitMessages(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_ = reader1.Close()

	// Second instance: a genuinely DIFFERENT unique group (simulating a
	// restart), must ALSO see the message from FirstOffset -- this is
	// exactly what a fixed shared group name would break.
	group2 := uniqueConsumerGroup()
	if group1 == group2 {
		t.Fatal("expected uniqueConsumerGroup to produce distinct ids across calls")
	}
	reader2 := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: group2, StartOffset: kafkago.FirstOffset})
	defer func() { _ = reader2.Close() }()

	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readCancel()
	msg, err := reader2.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("second (independent) reader failed to see the same message: %v", err)
	}
	if msg.Offset != 0 {
		t.Fatalf("expected the second instance to replay from offset 0, got offset %d", msg.Offset)
	}
}
