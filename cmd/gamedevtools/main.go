package main

import (
	"fmt"
	"os"

	"github.com/CyberCute-Studio/GameDevTools/internal/app"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ui.AppName, err)
		os.Exit(1)
	}
}
