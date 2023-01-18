package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Lambels/go-dialogue"
)

func main() {
	d := &dialogue.Dialogue{
		Prefix: "(echo) ",
		R:      os.Stdin,
		W:      os.Stdout,
	}

	d.RegisterCommands(
		&dialogue.Command{
			Name:    "echo",
			Exec:    exec,
		},
	)

	c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt)

    go func() {
        <-c
        log.Println("gracefully shutting down dialogue with a 5 second timeout...")
        
        ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
        defer cancel()

        d.Shutdown(ctx)
    }()

    if err := d.Open(); err != nil {
        log.Fatal(err)
    }
}

func exec(_ *dialogue.CallChain, args []string) error {
    log.Println(strings.Join(args, " "))
    return nil
}








