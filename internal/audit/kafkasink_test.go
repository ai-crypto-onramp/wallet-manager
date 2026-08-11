package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/wallet-manager/internal/storage"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func TestNewKafkaSinkEmptyBrokers(t *testing.T) {
	s := NewKafkaSink(nil)
	if s == nil {
		t.Fatal("expected non-nil sink")
	}
	if s.writer != nil {
		t.Error("expected nil writer for empty brokers")
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close on nil writer should be nil, got %v", err)
	}
}

func TestNewKafkaSinkWithBrokers(t *testing.T) {
	s := NewKafkaSink([]string{"broker1:9092", "broker2:9092"})
	if s == nil || s.writer == nil {
		t.Fatal("expected non-nil writer")
	}
	if s.writer.Topic != AuditTopic {
		t.Errorf("expected topic %s, got %s", AuditTopic, s.writer.Topic)
	}
	_ = s.Close()
}

func TestKafkaSinkDeliverNotConnected(t *testing.T) {
	s := &KafkaSink{}
	if err := s.Deliver(context.Background(), []*storage.AuditOutboxEvent{}); err == nil {
		t.Error("expected not-connected error for nil writer")
	}
}

func TestKafkaSinkDeliverWriteError(t *testing.T) {
	s := &KafkaSink{writer: &kafka.Writer{}}
	ev := &storage.AuditOutboxEvent{
		ID: uuid.New(), EventID: uuid.New(), EventType: "x", Payload: []byte(`{"a":1}`),
		CreatedAt: time.Now(),
	}
	if err := s.Deliver(context.Background(), []*storage.AuditOutboxEvent{ev}); err == nil {
		t.Error("expected write error (no broker reachable)")
	}
}

func TestBuildEnvelopeWithWalletID(t *testing.T) {
	wID := uuid.New()
	ev := &storage.AuditOutboxEvent{
		ID:        uuid.New(),
		EventID:   uuid.New(),
		WalletID:  &wID,
		EventType: "wallet.created",
		Payload:   []byte(`{"amount":"100"}`),
		Seq:       1,
		CreatedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	env, key, err := buildEnvelope(ev)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if key != ev.EventID.String() {
		t.Errorf("expected key=%s, got %s", ev.EventID, key)
	}
	if env["schema_version"] != "1" {
		t.Errorf("expected schema_version=1, got %v", env["schema_version"])
	}
	if env["action"] != "wallet.created" {
		t.Errorf("expected action=wallet.created, got %v", env["action"])
	}
	if env["target_id"] != wID.String() {
		t.Errorf("expected target_id=%s, got %v", wID, env["target_id"])
	}
	if env["source_service"] != "wallet-management" {
		t.Errorf("expected source_service=wallet-management, got %v", env["source_service"])
	}
	if env["actor_id"] != "wallet-management" {
		t.Errorf("expected actor_id=wallet-management, got %v", env["actor_id"])
	}
	if env["target_type"] != "wallet" {
		t.Errorf("expected target_type=wallet, got %v", env["target_type"])
	}
	hash, ok := env["payload_hash"].(string)
	if !ok || hash == "" || hash[:7] != "sha256:" {
		t.Errorf("expected payload_hash=sha256:..., got %v", env["payload_hash"])
	}
	ts, ok := env["ts"].(string)
	if !ok || ts == "" {
		t.Errorf("expected non-empty ts, got %v", env["ts"])
	}
	raw, ok := env["payload"].(json.RawMessage)
	if !ok {
		t.Errorf("expected payload as json.RawMessage, got %T", env["payload"])
	} else if string(raw) != `{"amount":"100"}` {
		t.Errorf("unexpected payload: %s", raw)
	}
}

func TestBuildEnvelopeNilWalletID(t *testing.T) {
	ev := &storage.AuditOutboxEvent{
		ID:        uuid.New(),
		EventID:   uuid.Nil,
		WalletID:  nil,
		EventType: "system",
		Payload:   []byte(`{}`),
		CreatedAt:  time.Now(),
	}
	env, key, err := buildEnvelope(ev)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if env["target_id"] != "" {
		t.Errorf("expected empty target_id for nil wallet, got %v", env["target_id"])
	}
	if key == "" || key == uuid.Nil.String() {
		t.Error("expected new uuid for nil EventID")
	}
	if env["id"] != key {
		t.Errorf("expected env id == key, got env=%v key=%s", env["id"], key)
	}
}

func TestBuildEnvelopeEmptyPayload(t *testing.T) {
	ev := &storage.AuditOutboxEvent{
		ID:        uuid.New(),
		EventID:   uuid.New(),
		EventType: "noop",
		Payload:   nil,
		CreatedAt: time.Now(),
	}
	env, _, err := buildEnvelope(ev)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if env["payload"] != nil {
		t.Errorf("expected nil payload, got %v", env["payload"])
	}
}

func TestKafkaSinkClose(t *testing.T) {
	s := &KafkaSink{}
	if err := s.Close(); err != nil {
		t.Errorf("Close on nil writer: %v", err)
	}
}

func TestKafkaSinkPingNotConfigured(t *testing.T) {
	s := &KafkaSink{}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error when not configured")
	}
}

func TestKafkaSinkPingUnreachable(t *testing.T) {
	s := NewKafkaSink([]string{"127.0.0.1:1"})
	defer s.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error when broker unreachable")
	}
}

var _ = errors.New