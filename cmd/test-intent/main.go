package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"github.com/openlibrecommunity/olcrtc/mobile"
)

func main() {
	secret := os.Args[1]
	endpoint := os.Args[2]
	intent, err := mobile.BuildSignedRoomIntent(secret, "test-room-debug", 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Signed intent (secret=%s...): %s\n", secret[:8], intent[:60])
	resp, err := http.Post(endpoint+"/api/room-intent", "application/json", bytes.NewReader([]byte(intent)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP %d: %s\n", resp.StatusCode, string(body))
}
