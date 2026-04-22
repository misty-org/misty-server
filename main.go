package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	server, err := CreateServer()
	if err != nil {
		panic(err)
	}

	if err := server.Database.Start(); err != nil {
		panic(err)
	}
	defer server.Database.Stop()

	if err := server.MountHandlers(); err != nil {
		panic(err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Misty server running on :%s", port)
	if err := http.ListenAndServe(":"+port, server.Router); err != nil {
		panic(err)
	}
}
