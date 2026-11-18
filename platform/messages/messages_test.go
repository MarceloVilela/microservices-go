package messages_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/MarceloVilela/microservices-go/platform/messages"
)

func TestPhotoProcessContrato(t *testing.T) {
	m := messages.PhotoProcess{V: messages.Version, MessageID: "7", BatchID: "b", PhotoID: "p", SourceURL: "https://picsum.photos/id/1/1/1"}
	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"message_id":"7","batch_id":"b","photo_id":"p","source_url":"https://picsum.photos/id/1/1/1"}`
	if string(got) != want {
		t.Errorf("json = %s\nquer   %s", got, want)
	}
}

func TestBatchCompletedContrato(t *testing.T) {
	m := messages.BatchCompleted{V: messages.Version, MessageID: "m", BatchID: "b", Total: 3, Failed: 1}
	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"message_id":"m","batch_id":"b","total":3,"failed":1}`
	if string(got) != want {
		t.Errorf("json = %s\nquer   %s", got, want)
	}
}

func TestNewIDEhUUIDv4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 100 {
		id := messages.NewID()
		if !re.MatchString(id) || seen[id] {
			t.Fatalf("id inválido ou repetido: %q", id)
		}
		seen[id] = true
	}
}
