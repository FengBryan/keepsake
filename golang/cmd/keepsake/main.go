package main

import (
	"github.com/replicate/keepsake/golang/pkg/cli"
	"github.com/replicate/keepsake/golang/pkg/console"
)

func main() {
	cmd, err := cli.NewRootCommand()
	if err != nil {
		console.Fatal("%s", err)
	}

	if err = cmd.Execute(); err != nil {
		console.Fatal("%s", err)
	}
}
