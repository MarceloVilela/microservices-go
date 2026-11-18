// Package messages define o contrato JSON das mensagens do broker.
// O campo v versiona o formato; mudança incompatível incrementa Version.
package messages

import (
	"crypto/rand"
	"fmt"
)

// Version é a versão atual do formato.
const Version = 1

// NewID gera um UUIDv4 para message_id.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand não falha em sistemas suportados
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// PhotoProcess é o job publicado em photo.process (um por foto).
type PhotoProcess struct {
	V         int    `json:"v"`
	MessageID string `json:"message_id"`
	BatchID   string `json:"batch_id"`
	PhotoID   string `json:"photo_id"`
	SourceURL string `json:"source_url"`
}

// BatchCompleted é o evento publicado uma vez por lote terminado (N6, ADR 0014).
type BatchCompleted struct {
	V         int    `json:"v"`
	MessageID string `json:"message_id"`
	BatchID   string `json:"batch_id"`
	Total     int    `json:"total"`
	Failed    int    `json:"failed"`
}

// Cabeçalhos AMQP usados pelo worker ao republicar.
const (
	// HeaderAttempt é quantos degraus de retry a mensagem já percorreu (0 na primeira entrega).
	HeaderAttempt = "x-attempt"
	// HeaderFailureClass e HeaderFailureReason acompanham a mensagem na DLQ.
	HeaderFailureClass  = "x-failure-class"
	HeaderFailureReason = "x-failure-reason"
)
