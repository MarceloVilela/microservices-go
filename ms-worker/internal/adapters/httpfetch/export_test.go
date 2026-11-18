package httpfetch

import "net/netip"

// Blocked expõe a política de IP para o teste externo.
func Blocked(ip netip.Addr) bool { return blocked(ip) }
