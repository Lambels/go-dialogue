package dialogue

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// ErrDialogueClosed is returned by Open() indicating a closed dialogue.
var ErrDialogueClosed = errors.New("dialogue: dialogue closed")

// Dialogue describes a back and forth discussion between the provided reader and writer.
type Dialogue struct {
	// Prefix is an optional but recommended field which gets outputed before every read from R.
	Prefix string

	// R is the source of the "conversation" where the user types in the message and the message is mapped to a command or
	// to the CommandNotFound handler. R isnt used raw, its wrapped by the PreamptiveReader to provide preamptive reads which
	// are cancellable by the base context.
	R io.Reader

	// W is the destination of the "conversation", it is used by all the default implementations as the destination of messages
	// which include: the default CommandNotFound and HelpCmd implementations.
	W io.Writer

	// CommandNotFound handels commands which arent mapped to anything. The ctx is the base context and the args are the full
	// fields read from R including the command name.
	//
	// If nil the default CommandNotFound will be used which will call FormatHelp.
	CommandNotFound func(ctx context.Context, args []string) error

	// HelpCmd is an optional field, it creates a help command for you which doesent require any more over head then providing the
	// command name, everything else is handeled by default.
	//
	// The implementation of the help command takes the following structure:
	//
	// <HelpCmd> [-n command-name]
	//
	// The implementation takes advantage of the FormatHelp field and wraps around it, calling it:
	//
	// 1. When -n flag is provided:
	//
	// FormatHelp(valueFromNFlag, cmds)
	//
	// 2. When -n isnt provided:
	//
	// FormatHelp("", cmds)
	//
	// Again HelpCmd is optional and if the default implementation doesnt suit your needs, feel free to register your own implementation
	// of a help command using your own *dialogue.Command.
	HelpCmd string

	// QuitCmd is an optional field, it creates a quit command for you and registers it to the dialogue. It exits the dialogue with
	// the ErrDialogueClosed error.
	//
	// Again QuitCmd is optional and is intended to save you boilerplate code, if you are looking for a more customisable quit command
	// register you own *dialogue.Command which quits the dialogue however you want.
	QuitCmd string

	// FormatHelp is an optional field called by the default implementations of HelpCmd and CommandNotFound.
	//
	// There is no guarantee that the provided command name is in the commands map but its always guaranteed that the cmds map
	// will consist of the current available commands in the dialogue.
	FormatHelp func(cmd string, cmds map[string]*Command) string

	// CommandContext optinally specifies a function to set the context for a command. The provided context is derived from the
	// base context and its up to the implementation of the function to wrap or not the returned context with the base context
	// but if not wrapped, no cancelation can be propagated to the command.
	CommandContext func(context.Context, string) context.Context

	mu       sync.Mutex          // protects the fields below.
	ctx      context.Context     // ctx is the base context used for cancelation.
	cancel   context.CancelFunc  // cancel cancels the base context.
	pr       *PreamptiveReader   // pr is the wrapped preamptive reader. (it is wrapped around R)
	commands map[string]*Command // commands is a mapping of the command name to command.
	running  bool                // indicates if the current dialogue is running.
	close    chan chan struct{}  // used to send acknowledgement signals between the close calls and the processing go routine.
}

// Open initialises the dialogue and listens for tokens (provided by the default bufio.Scanner) and maps them to commands.
//
// Open always returns non nil errors. After a call to Shutdown or Close the returned error is ErrDialogueClosed.
//
// IMPORTANT:
//
// You can open previously closed dialogues but be aware of the underlaying preamptive reader since it will always be binded to
// the initiall reader and may read messages from the past transaction.
func (d *Dialogue) Open() error {
	if err := d.init(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(d.pr)
	for {
		// acknowledge any close signals before commiting to a write call.
		if err := d.exit(nil); err != nil {
			return err
		}

		if _, err := d.W.Write([]byte(d.Prefix)); err != nil {
			return d.exit(err)
		}

		advance := scanner.Scan()

		if !advance {
			return d.exit(scanner.Err())
		}

		token := scanner.Text()
		fields := strings.Fields(token)

		if len(fields) == 0 {
			continue
		}

		err := d.dispatchHandler(fields[0], fields[1:])
		if err != nil {
			return d.exit(err)
		}
	}
}

// exit locks the dialogue in closing state, it first tries to acknowledge any closing signals before returning the provided
// error.
//
// If it acknowledges any exit errors it returns ErrDialogueClosed.
func (d *Dialogue) exit(err error) error {
	// acquire mutex to make sure there is no race condition between sending an acknowledgement and recieving it.
	d.mu.Lock()
	defer d.mu.Unlock()

	select {
	case ack := <-d.getCloseLocked():
		ack <- struct{}{} // acknowledge we are closing.

		// dont exit before context is cancelled, we want to make sure that both external cancelling methods (Shutdown and Close)
		// and the Open go routine exit after the base context is cancelled and the d.closing flag is set to true. This is important
		// to be synchronised because any calls after Open or Shutdown / Close exit which access the underlaying preamptive reader
		// have to access it via a cancelled context to provide expected behaviour.
		<-d.ctx.Done()
		return ErrDialogueClosed
	default:
	}

	if err != nil {
		d.cancel() // cancel context to propagate the closing signal to the preamptive reader.
		d.running = false
	}

	return err
}

// dispatchHandler dispatches the handler for cmd if it exits or the not found handler.
// finally it returns any error from the handlers.
func (d *Dialogue) dispatchHandler(cmd string, args []string) error {
	command, ok := d.commands[cmd]
	if !ok {
		// accomodate the cmd name in the args to the not found handler.
		tmp := make([]string, 1, len(args)+1)
		tmp[0] = cmd
		copy(tmp[1:], args)

		return d.CommandNotFound(d.ctx, tmp)
	}

	cmdCtx := d.ctx
	if cc := d.CommandContext; cc != nil {
		cmdCtx = cc(cmdCtx, cmd)
		if cmdCtx == nil {
			return errors.New("CommandContext returned nil context")
		}
	}

	callChain, err := command.parse(args)
	// error returned because flag set uses continue on error, dont report error back to the dispatcher to "continue on error".
	if err != nil {
		return nil
	}

	defer callChain.clean()
	return callChain.AdvanceExec(0, cmdCtx) // start call chain.
}

func (d *Dialogue) init() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.commands) == 0 {
		return errors.New("dialogue: no commands")
	}

	// set the quit command.
	if _, ok := d.commands[d.QuitCmd]; d.QuitCmd != "" && !ok {
		d.commands[d.QuitCmd] = &Command{
			Name:      d.QuitCmd,
			HelpShort: "quits the dialogue abruptly",
			Exec: func(_ *CallChain, _ []string) error {
				// no point in setting running state to false in here to prevent other calls to Shutdown or Close since in the end
				// they all play into the same side effects: ErrDialogueClosed returned from Open(), context cancelled and running
				// state set to false safely.
				return ErrDialogueClosed
			},
		}
	}

	// set the help command.
	if _, ok := d.commands[d.HelpCmd]; d.HelpCmd != "" && !ok {
		fs := flag.NewFlagSet(d.HelpCmd, flag.ExitOnError)
		nParam := fs.String("n", "", "specifies the command name you want help on")

		d.commands[d.HelpCmd] = &Command{
			Name:      d.HelpCmd,
			Structure: fmt.Sprintf("%v [-n <command-name>]", d.HelpCmd),
			HelpShort: "outputs the help prompt for all commands or a specified command via the -n flag",
			HelpLong: `help formats a short version of help prompts for all available commands when ran without the -n flag,
optinally you can provide the -n flag to get a more thorough help prompt for a specific command indicated by the name passed after
the -n flag.`,
			FlagSet: fs,
			Exec: func(_ *CallChain, _ []string) error {
				_, err := fmt.Fprintf(d.W, d.FormatHelp(*nParam, d.commands))
				return err
			},
		}
	}

	if err := d.initCommandsLocked(); err != nil {
		return err
	}

	// check if context doesnt exist or previous context expired (this means the dialogue is being reused).
	if d.ctx == nil || d.ctx.Err() != nil {
		d.ctx, d.cancel = context.WithCancel(context.Background())

		// if there is an existing preamptive reader, continue using it with the new context since it may still have
		// state.
		if d.pr != nil {
			d.pr.ctx = d.ctx
		}
	}

	if d.pr == nil {
		d.pr = NewPreamptiveReader(d.ctx, d.R)
	}

	if d.FormatHelp == nil {
		d.FormatHelp = defaultHelpFormater
	}

	if d.CommandNotFound == nil {
		d.CommandNotFound = d.defaultCmdNotFound
	}

	d.running = true
	return nil
}

func (d *Dialogue) initCommandsLocked() error {
	for _, cmd := range d.commands {
		if err := cmd.init(); err != nil {
			return err
		}
	}

	return nil
}

func (d *Dialogue) getCloseLocked() chan chan struct{} {
	if d.close == nil {
		d.close = make(chan chan struct{}, 1)
	}

	return d.close
}

func (d *Dialogue) signalClosingLocked() <-chan struct{} {
	d.running = false
	ackChan := make(chan struct{}) // unbuffered to provide acknowledgement synchronisation.

	// signal close.
	d.getCloseLocked() <- ackChan
	return ackChan
}

// Close imidiately cancels the base context and always returns nil.
func (d *Dialogue) Close() error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	notify := d.signalClosingLocked()
	d.mu.Unlock()

	d.cancel()
	<-notify
	return nil
}

// Shutdown gracefully shuts down the dialogue waiting for the current Read() or Command.Exec() opperation
// without any interuption. It waits indefenetly for the current transaction to finish or till the provided context
// expires. When the context expires the underlaying context is cancelled and the rest of the opperation behaves like a normal
// call to Close().
func (d *Dialogue) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	notify := d.signalClosingLocked()
	d.mu.Unlock()

	select {
	case <-notify:
		d.cancel()
		return nil
	case <-ctx.Done():
		d.cancel()
		<-notify
		return ctx.Err()
	}
}

// RegisterCommands registers the provided commands to the dialogue. If the dialogue is running the call is no-op. RegisterCommands
// can be called even after a call to Close() or Shutdown() as long as the dialogue isnt running.
func (d *Dialogue) RegisterCommands(cmds ...*Command) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.running {
		return
	}

	if d.commands == nil {
		d.commands = make(map[string]*Command)
	}

	for _, c := range cmds {
		d.commands[c.Name] = c
	}
}

// PreamptiveReader returns the underlaying preamptive reader used by the dialogue. The underlaying preamptive reader can be
// used to drain any remaining read / buffer or to merge with other readers after closing a dialogue.
//
// If the dialogue is currently running the call is no-op.
//
// IMPORTANT:
//
// If you plan on re opening the dialogue make sure to be responsible to what calls you are making to the preamptive reader since
// you can only have one read at a time on the preamptive reader and you can cause un intended errors.
func (d *Dialogue) PreamptiveReader() *PreamptiveReader {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.running {
		return nil
	}

	return d.pr
}

// Visit visits all the commands available in the dialogue at the time of calling in lexicographical order.
func (d *Dialogue) Visit(fn func(*Command)) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, cmd := range sortCommands(d.commands) {
		fn(cmd)
	}
}

func sortCommands(commands map[string]*Command) []*Command {
	out := make([]*Command, len(commands))

	var i int
	for _, c := range commands {
		out[i] = c
		i++
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})

	return out
}

func (d *Dialogue) defaultCmdNotFound(_ context.Context, args []string) error {
	fmt.Fprintf(d.W, "Command: %v not found\n", args[0])
	fmt.Fprint(d.W, d.FormatHelp("", d.commands))

	return nil
}

func defaultHelpFormater(cmd string, cmds map[string]*Command) (out string) {
	if cmd == "" { // format all commands if no cmd name provided.
		var b strings.Builder
		for _, cmd := range sortCommands(cmds) {
			b.WriteString(cmd.FormatHelp(cmd, false))
		}

		out = b.String()
	} else {
		c, ok := cmds[cmd]
		if !ok {
			return "command not found\n"
		}

		out = c.FormatHelp(c, true)
	}

	return out
}
