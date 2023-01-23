package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Lambels/go-dialogue"
)

func main() {
	d := &dialogue.Dialogue{
		Prefix:  "(echo) ",
		R:       os.Stdin,
		W:       os.Stdout,
		HelpCmd: "help", // will generate the help command for us, it will be accesible under the "help" keyword.
	}

	fs := flag.NewFlagSet("echo", flag.ContinueOnError)
	// n will be set to the default value by the go-dialogue tool after each itteration of the
	// repeat command by default, no need to reset the value yourself.
	n := fs.Int("n", 1, "sets the number of repetitions of the output")

	repeat := func(_ *dialogue.CallChain, args []string) error {
		for i := 0; i < *n; i++ {
			_, err := fmt.Fprintln(d.W, strings.Join(args, " "))
			if err != nil {
				return err
			}
		}

		return nil
	}

	d.RegisterCommands(
		&dialogue.Command{
			Name:      "echo",
			Structure: "echo [-n repeat] <args>",
			HelpShort: "echo will repeat the args -n times",
			HelpLong:  "echo takes in the provided arguments and writes them back -n times (defaults to 1) to the writer",
			FlagSet:   fs,
			Exec:      repeat,
		},
	)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	go func() {
		<-c
		log.Println()
		log.Println("gracefully shutting down dialogue with a 5 second timeout...")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		d.Shutdown(ctx)
	}()

	if err := d.Open(); err != nil {
		log.Fatal(err)
	}
}
