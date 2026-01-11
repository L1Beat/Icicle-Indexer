package cmd

import (
	"icicle/pkg/api"
	"icicle/pkg/chwrapper"
	"log"
)

func RunAPI(port int) {
	log.Printf("Starting API server...")

	// Connect to ClickHouse
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	// Create and start API server
	server := api.NewServer(conn)
	if err := server.Start(port); err != nil {
		log.Fatalf("API server failed: %v", err)
	}
}
