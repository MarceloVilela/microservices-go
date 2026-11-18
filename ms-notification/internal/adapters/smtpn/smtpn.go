// Package smtpn é o destino opcional da notificação: um e-mail por lote via SMTP (ADR 0020).
// Sem autenticação nem TLS: serve ao Mailpit do desenvolvimento, não a e-mail real.
package smtpn

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// Notifier envia o e-mail.
type Notifier struct {
	Addr    string // host:porta
	From    string
	To      string
	Timeout time.Duration
}

// Name identifica o destino nos logs de falha.
func (Notifier) Name() string { return "smtp" }

// Notify envia um e-mail; qualquer falha (conexão, recusa, prazo) volta como erro, nunca pânico.
func (n Notifier) Notify(ctx context.Context, m messages.BatchCompleted) error {
	if strings.ContainsAny(n.From+n.To, "\r\n") {
		return errors.New("remetente ou destinatário com quebra de linha")
	}
	timeout := n.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", n.Addr)
	if err != nil {
		return fmt.Errorf("conectar ao SMTP: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// o prazo do ctx também vale para um servidor que para de responder no meio da conversa
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	host, _, _ := net.SplitHostPort(n.Addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("saudação SMTP: %w", err)
	}
	if err := c.Mail(n.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(n.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(message(n, m)); err != nil {
		return fmt.Errorf("escrever mensagem: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("fechar DATA: %w", err)
	}
	// o DATA já foi aceito (250): erro no QUIT não desfaz a entrega
	_ = c.Quit()
	return nil
}

func message(n Notifier, m messages.BatchCompleted) []byte {
	subject := mime.QEncoding.Encode("utf-8", fmt.Sprintf("Lote %s concluído", m.BatchID))
	body := fmt.Sprintf("O lote %s terminou.\r\nFotos: %d\r\nProcessadas: %d\r\nFalhas: %d\r\n", m.BatchID, m.Total, m.Total-m.Failed, m.Failed)
	return []byte("From: " + n.From + "\r\nTo: " + n.To + "\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
}
