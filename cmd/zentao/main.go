package main

import (
	"os"

	"github.com/rioliu/zentao-cli-go/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
