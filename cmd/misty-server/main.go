package main

import (
	"log"
	"os"

	"github.com/misty-org/misty-server/internal/app"
)

func main() {
	if err := loadJournalServerEnv(os.Getenv("MISTY_JOURNAL_SERVER_ENV_FILE")); err != nil {
		log.Fatal(err)
	}
	app.Run()
}
