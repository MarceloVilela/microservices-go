package messages_test

import (
	"encoding/json"
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
