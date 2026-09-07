package main

import (
	"flag"
	"log"
	"os"

	"agent/internal/app"
)

func main() {
	health := flag.Bool("health", false,
		"ask the agent at AGENT_ADDR whether it is answering, then exit; this is the container's health check")
	flag.Parse()

	if *health {
		// Exit status is the whole answer, so the error goes to stderr and
		// nothing is logged on success.
		if err := app.Health(app.LoadConfig().Addr); err != nil {
			log.SetFlags(0)
			log.Println(err)
			os.Exit(1)
		}
		return
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
