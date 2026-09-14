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
	confine := flag.String("confine", "",
		"internal: restrict this process to the given policy and become the command after --")
	flag.Parse()

	// The agent is its own sandbox wrapper on Linux: it re-runs itself in front
	// of every tool, and this is that second run. It never returns — it becomes
	// the tool, or it exits.
	if *confine != "" {
		app.Confine(*confine, flag.Args())
		return
	}

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
