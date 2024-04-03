package main

import (
	"github.com/replicate/keepsake/golang/pkg/cli"
	"github.com/replicate/keepsake/golang/pkg/console"
)

func main() {
	cmd := cli.NewDaemonCommand()
	if err := cmd.Execute(); err != nil {
		console.Fatal("%s", err)
	}
}
