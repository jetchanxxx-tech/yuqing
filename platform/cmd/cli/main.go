package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: yuging-cli <command> [args...]")
		fmt.Println("commands: migrate, provision-tenant")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "migrate":
		fmt.Println("migrate: not yet implemented")
	case "provision-tenant":
		fmt.Println("provision-tenant: not yet implemented")
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
