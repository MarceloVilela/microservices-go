package httpfetch

import (
	"net/netip"
	"time"
)

// Blocked expõe a política de IP para o teste externo.
func Blocked(ip netip.Addr) bool { return blocked(ip) }

// ParseRetryAfter expõe a leitura do cabeçalho para o teste externo.
func ParseRetryAfter(v string, now time.Time) time.Duration { return parseRetryAfter(v, now) }
