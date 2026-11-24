package main

import (
	"log"

	"github.com/misty-org/misty-server/internal/app"
	"github.com/misty-org/misty-server/internal/platform/config"
)

func main() {
	if err := config.ApplyServerDefaults(); err != nil {
		log.Fatal(err)
	}
	app.RunCollab()
}
