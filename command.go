package dialogue

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
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

// CallChain represents a linked list pathed through the command tree following a path of execution.
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

// clean sets all used flags in the call chain back to their default values.
func (c *CallChain) clean() {
	for _, cmd := range *c {
		cmd.FlagSet.Visit(func(f *flag.Flag) {
			f.Value.Set(f.DefValue)
		})
	}
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
	//
	// focus = true indicates that the FormatHelp call was called for this command specifically, if focus = false then
	// the FormatHelp call was called in a batch with other calls and shouldnt be very specific.
	FormatHelp func(cmd *Command, focus bool) string

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

// parse the command trees recursively building the command chain.
func (c *Command) parse(args []string) (*CallChain, error) {
	if err := c.FlagSet.Parse(args); err != nil {
		return nil, err
	}

	cmdArgs := c.FlagSet.Args()

	// search sub commands in command args.
	if len(cmdArgs) > 0 {
		for _, subCmd := range c.SubCommands {
			for i, arg := range args {
				// found match, truncate arguments and pass the rest to the next.
				if strings.EqualFold(arg, subCmd.Name) {
					c.args = cmdArgs[:i]
					cc, err := subCmd.parse(cmdArgs[i+1:]) // exclude the sub command name.
					if err != nil {
						return nil, err
					}

					*cc = append(*cc, c)
					return cc, nil
				}
			}
		}
	}

	// BASE CASE:
	// Exhausted all arguments and found no matches to any sub commands, return the current command.
	c.args = cmdArgs
	return &CallChain{c}, nil
}

// init checks if the command has all the provided fields set in order to run, it only runs on dialogue startup.
func (c *Command) init() error {
	if c.Name == "" {
		return ErrNoName
	}

	if c.Exec == nil {
		return ErrNoExec{c.Name}
	}

	if c.FlagSet == nil { // provide flagset for help flag.
		c.FlagSet = flag.NewFlagSet(c.Name, flag.ExitOnError)
	}

	if c.FormatHelp == nil {
		c.FormatHelp = defaultCommandHelpFormater
	}

	c.FlagSet.Usage = func() { fmt.Fprintln(c.FlagSet.Output(), c.FormatHelp(c, true)) }

	return nil
}

// defaultCommandHelpFormater is used as the default FormatHelp handler.
func defaultCommandHelpFormater(c *Command, focus bool) string {
	var b strings.Builder

	// c out of focus, return the short help.
	if !focus {
		buildHelpShort(&b, c)
		return b.String()
	}

	if c.Structure != "" {
		b.WriteString(c.Structure)
	} else {
		b.WriteString(c.Name)
	}

	b.WriteString("\n\n")

	if c.HelpLong != "" {
		b.WriteString(c.HelpLong)
		b.WriteString("\n\n")
	}

	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)

	// format flags:
	if nFlags(c.FlagSet) > 0 {
		b.WriteString("FLAGS\n")
		c.FlagSet.VisitAll(func(f *flag.Flag) {
			defV := f.DefValue
			var space string
			if defV != "" {
				space = "="
			}

			fmt.Fprintf(tw, "-%s%s%s\t%s\n", f.Name, space, defV, f.Usage)
		})

		tw.Flush()
		b.WriteByte('\n')
	}

	// format sub commands:
	if len(c.SubCommands) > 0 {
		b.WriteString("SUBCOMMANDS\n")

		for _, sCmd := range c.SubCommands {
			buildHelpShort(tw, sCmd)
		}

		tw.Flush()
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String()) + "\n"
}

func nFlags(fs *flag.FlagSet) (n int) {
	fs.VisitAll(func(f *flag.Flag) { n++ })
	return n
}

// buildHelpShort builds the out of focus / short version of the help text.
//
// It prefers the command structure over the command name.
func buildHelpShort(w io.Writer, c *Command) {
	name := c.Name

	if c.Structure != "" {
		name = c.Structure
	}

	fmt.Fprintf(w, "%s\t%s\n", name, c.HelpShort)
}
