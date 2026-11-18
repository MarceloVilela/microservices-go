// Package messages define o contrato JSON das mensagens do broker.
// O campo v versiona o formato; mudança incompatível incrementa Version.
package messages

// Version é a versão atual do formato.
const Version = 1

// PhotoProcess é o job publicado em photo.process (um por foto).
type PhotoProcess struct {
	V         int    `json:"v"`
	MessageID string `json:"message_id"`
	BatchID   string `json:"batch_id"`
	PhotoID   string `json:"photo_id"`
	SourceURL string `json:"source_url"`
}
