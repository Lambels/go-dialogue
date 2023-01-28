package dialogue

import (
	"flag"
	"log"
	"reflect"
	"testing"
)

var testCommand = &Command{
	Name:      "test",
	Structure: "test [args]",
	HelpShort: "short help format",
	HelpLong:  "long help format",
	Exec: func(chain *CallChain, args []string) error {
		log.Println("test cmd called")
		return nil
	},
	FormatHelp: func(cmd *Command, focus bool) string {
		if focus {
			return "focused"
		}

		return "not focused"
	},
}

func TestParse(t *testing.T) {
	cmd1 := &Command{
		Name:    "cmd1",
		FlagSet: flag.NewFlagSet("testing", flag.ContinueOnError),
	}
	cmd2 := &Command{
		Name:    "cmd2",
		FlagSet: flag.NewFlagSet("testing", flag.ContinueOnError),
	}
	cmd3 := &Command{
		Name:    "cmd3",
		FlagSet: flag.NewFlagSet("testing", flag.ContinueOnError),
	}
	cmd4 := &Command{
		Name:    "cmd4",
		FlagSet: flag.NewFlagSet("testing", flag.ContinueOnError),
	}
	cmd5 := &Command{
		Name:    "cmd5",
		FlagSet: flag.NewFlagSet("testing", flag.ContinueOnError),
	}

	cmd1.SubCommands = []*Command{cmd2, cmd3, cmd4}
	cmd2.SubCommands = []*Command{cmd3}
	cmd3.SubCommands = []*Command{cmd4, cmd5}

	cmd4.SubCommands = []*Command{cmd5}
	cmd5.SubCommands = []*Command{cmd4}

	// cmd1 -> cmd2, cmd3, cmd4
	// cmd2 -> cmd3
	// cmd3 -> cmd4, cmd5
	// cmd4 -> cmd5
	// cmd5 -> cmd4

	type testCase struct {
		rootCmd  *Command
		args     []string
		expected *CallChain
	}

	testCases := []testCase{
		{cmd1, []string{"not existing"}, &CallChain{cmd1}},
		{cmd1, []string{"cmd2", "cmd3", "cmd5"}, &CallChain{cmd5, cmd3, cmd2, cmd1}},
		{cmd2, []string{"cmd3", "cmd4"}, &CallChain{cmd4, cmd3, cmd2}},
		{cmd2, []string{"cmd1"}, &CallChain{cmd2}},
		{cmd3, []string{"cmd3"}, &CallChain{cmd3}},
		{cmd4, []string{"cmd5", "cmd4", "cmd5", "cmd4"}, &CallChain{cmd4, cmd5, cmd4, cmd5, cmd4}},
	}

	for _, tc := range testCases {
		cc, err := tc.rootCmd.parse(tc.args)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(*cc, *tc.expected) {
			t.Fatalf("mismatch for %v with args %v between expected call chain: %v but got %v", *tc.rootCmd, tc.args, *tc.expected, *cc)
		}
	}
}


