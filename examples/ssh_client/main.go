package main

import (
	"fmt"
	"io"
	"os"

	"multicrum/ssh_client"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s user@host[:port]\n", os.Args[0])
		os.Exit(2)
	}

	client, err := ssh_client.New(ssh_client.Options{
		Target:         os.Args[1],
		UseDefaultKeys: true,
		UseAgent:       true,
	})
	if err != nil {
		panic(err)
	}

	remote, err := client.Start(120, 40)
	if err != nil {
		panic(err)
	}
	defer remote.Close()

	go func() {
		_, _ = io.Copy(os.Stdout, remote)
	}()
	_, _ = io.Copy(remote, os.Stdin)
}
