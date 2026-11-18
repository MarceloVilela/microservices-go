package slogn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-notification/internal/adapters/slogn"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

func TestNotifyEscreveUmaLinhaComOsTotais(t *testing.T) {
	var buf bytes.Buffer
	n := slogn.Notifier{Log: slog.New(slog.NewJSONHandler(&buf, nil))}
	if err := n.Notify(context.Background(), messages.BatchCompleted{BatchID: "b1", Total: 5, Failed: 2}); err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log não é JSON de uma linha: %v (%q)", err, buf.String())
	}
	if rec["evento"] != "batch.completed" || rec["batch_id"] != "b1" || rec["total"] != float64(5) || rec["failed"] != float64(2) || rec["done"] != float64(3) {
		t.Errorf("campos = %v", rec)
	}
}
