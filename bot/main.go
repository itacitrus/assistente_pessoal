// bot/main.go
package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: bot <command>")
		fmt.Println("Commands:")
		fmt.Println("  run        Start the WhatsApp bot")
		fmt.Println("  add-user   Add a new user")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runBot()
	case "add-user":
		addUser()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runBot() {
	log.Println("Bot starting... (not yet implemented)")
}

func addUser() {
	log.Println("Add user... (not yet implemented)")
}
