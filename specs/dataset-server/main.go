package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9099", "endereço (use loopback)")
	flag.Parse()
	s := New()
	log.Printf("dataset-server em http://%s (big-pixels.png=%d bytes)", *addr, len(s.bigPNG))
	log.Fatal(http.ListenAndServe(*addr, s.Handler()))
}
