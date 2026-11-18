package app

import "time"

// StepFor escolhe o degrau de retry (índice em ttls, crescente): o degrau natural é attempt;
// com Retry-After, o menor degrau cujo TTL cobre a espera pedida (arredondado para cima), nunca
// abaixo de attempt. Acima do último TTL usa o último degrau. Retry-After <= 0 é ignorado.
func StepFor(attempt int, retryAfter time.Duration, ttls []time.Duration) int {
	if len(ttls) == 0 {
		return 0
	}
	step := min(max(attempt, 0), len(ttls)-1)
	if retryAfter <= 0 {
		return step
	}
	for i := step; i < len(ttls); i++ {
		if ttls[i] >= retryAfter {
			return i
		}
	}
	return len(ttls) - 1
}
