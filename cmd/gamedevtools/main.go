package main

import (
	"fmt"
	"os"

	"github.com/CyberCute-Studio/GameDevTools/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "GameDevTools: %v\n", err)
		os.Exit(1)
	}
}
