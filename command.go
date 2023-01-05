package dialogue

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
)

// ErrNoExec is returned when a command in a call chain has no exec function therefor cant
// advance the chain.
type ErrNoExec struct {
	name string
}

func (e ErrNoExec) Error() string {
	return fmt.Sprintf("dialogue: %v has no exec function", e.name)
}

// ErrNoName identifies a command with no name.
var ErrNoName = errors.New("dialogue: command has no name")

// CallChain represents a linked list inside the command tree which is a path of execution.
//
// It starts inverted, the last command in the tree will be the first in the call chain.
type CallChain []*Command

// Advance advances the call chain n positions, it panics if n >= len(c) or n < 0.
func (c *CallChain) Advance(n int) {
	if n >= len(*c) || n < 0 {
		panic("cannot advance")
	}

	*c = (*c)[n:]
}

// AdvanceExec is a helper method which advances the chain n times and executes the nth
// command in the chain with the provided context returning the error from the call.
//
// If the context is nil context.Background will be used when calling Exec.
func (c *CallChain) AdvanceExec(n int, ctx context.Context) error {
	c.Advance(n)

	if ctx == nil {
		ctx = context.Background()
	}

	cmd := (*c)[0]
	cmd.ctx = ctx

	return cmd.Exec(c, cmd.args)
}

// Next peeks into the next command without advancing the chain, if there is no next command
// nil is returned. If there is the command is returned with the specified context.
//
// If the context is nil context.Background will be used.
func (c *CallChain) Next(ctx context.Context) *Command {
	if len(*c) <= 1 {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	cmd := (*c)[1]
	cmd.ctx = ctx
	return cmd
}

// GetCurrent gets the current command in the chain.
func (c *CallChain) GetCurrent() *Command {
	return (*c)[0]
}

// Command represents a parsable, executable and chainable instruction from the command line.
// It stores a flagset used to parse command line arguments, an exec function which is called
// upon execution, other commands in the form of sub commands which allows tree like branching and
// fields consumed by the DefaultHelpFunc to produce a clean help message.
type Command struct {
	// Name of the command. Used as its identifier when calling upon it
	// or when displayed in help / usage texts. This field is required.
	Name string

	// Structure displays the structure text for the command. This field isnt required but its
	// recommended. It is consumed by the DefaultHelpFunc and displayed at the top of the
	// help output. It should show the structure of optional or required flags of the command.
	Structure string

	// HelpLong displays thorough and contextual information about the usage of the command.
	// This field isnt required but its recommended. It is consumed by the DefaultHelpFunc and
	// displayed under the structure/name fields.
	HelpLong string

	// HelpShort displays short and consice text about the command. This field isnt required
	// but its recommended. It is consumed by the DefaultHelpFunc and displayed next to the names
	// of the sub commands.
	HelpShort string

	// HelpFunc consumes a command and outputs a help string for the command to FlagSet.Output().
	// The function is invoced by the -h or --help flag under the recieved command object. The HelpFunc
	// should be capable of consuming the Name, Structure, HelpLong, HelpShort, FlagSet and SubCommands
	// fields to generate a thorough output.
	HelpFunc func(*Command) string

	// SubCommands holds the sub commands accessible from the root command. This structure allows
	// commands to branch out like a tree.
	SubCommands []*Command

	// FlagSet is the core of the command and its used to parse the flags and sub commands.
	// This field is optional.
	FlagSet *flag.FlagSet

	// Exec is the main process of the command. This field is required. The responsabilities of the
	// exec function is to create any side effect, return an error if any and finally call on to the
	// next command via the call chain. This way you can controll the execution flow of your
	// commands and return any errors. Contextual information should be handeled by context parameter
	// when advancing the chain.
	Exec func(chain *CallChain, args []string) error

	// computated at command runtime.
	ctx  context.Context
	args []string
}

// Context fetches the context from the command. If the context is nil, context.Background will
// be returned.
func (c *Command) Context() context.Context {
	if c.ctx == nil {
		c.ctx = context.Background()
	}

	return c.ctx
}

// Parse parses a chain of commands returning a call chain and any error which occured during the
// parsing.
func (c *Command) parse(args []string) (*CallChain, error) {
	cc := make([]*Command, 0)
	ptr := c
	for ptr != nil {
		subCmd, err := ptr.parseNext(args)
		if err != nil {
			return nil, err
		}
		cc = append(cc, ptr)

		ptr = subCmd
		args = args[1:]
	}

	// inverse slice.
	for i, j := 0, len(cc)-1; i < j; i, j = i+1, j-1 {
		cc[i], cc[j] = cc[j], cc[i]
	}

	callChain := CallChain(cc)
	return &callChain, nil
}

// parseNext parses the current command and returns the next command to be parsed.
func (c *Command) parseNext(args []string) (*Command, error) {
	if c.Name == "" {
		return nil, ErrNoName
	}

	if c.FlagSet == nil {
		c.FlagSet = flag.NewFlagSet(c.Name, flag.ExitOnError)
	}

	if c.HelpFunc == nil {
		c.FlagSet.Usage = func() { fmt.Fprintln(c.FlagSet.Output(), DefaultHelp(c)) }
	}

	if err := c.FlagSet.Parse(args); err != nil {
		return nil, err
	}

	if c.Exec == nil {
		return nil, ErrNoExec{c.Name}
	}

	c.args = c.FlagSet.Args()
	if len(c.args) > 0 {
		for _, subCmd := range c.SubCommands {
			if strings.EqualFold(c.args[0], subCmd.Name) {
				return subCmd, nil
			}
		}
	}

	return nil, nil
}

// ParseAndRun is a helper function which parses the command along side with all its sub
// commands and runs the first command in the chain.
func (c *Command) parseAndRun(ctx context.Context, args []string) error {
	chain, err := c.parse(args)
	if err != nil {
		return err
	}

	return chain.AdvanceExec(0, ctx)
}

// TODO: implement
func DefaultHelp(c *Command) string {
	return ""
}

func parseRawCmd(cmd []string) (string, []string, error) {
	return cmd[0], cmd[1:], nil
}
