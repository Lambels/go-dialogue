package dialogue

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
)

// Dialogue describes a back and forth discussion between the provided reader and writer.
type Dialogue struct {
	// Prefix is an optional but recommended field which gets outputed before every read from R.
	Prefix string

	// R is the source of the "conversation" where the user types in the message and the message is mapped to a command or
	// to the CommandNotFound handler. R isnt used raw, its wrapped by the PreamptiveReader to provide preamptive reads which
	// are cancellable by the base context.
	R io.Reader

	// W is the destination where the Prefix is written.
	W io.Writer

	// CommandNotFound handels commands which arent mapped to anything. The ctx is the base context and the args are the full
	// fields read from R including the command name.
	//
	// If nil the default CommandNotFound will be used which will call FormatHelp.
	CommandNotFound func(ctx context.Context, args []string) error

	// FormatHelp formats the help message for all the current commands. If left nil the default help formater will be used.
	//
	//TODO: Implement default command and format handler.
	//TODO: Have specfic keywords for help.
	//TODO: Have a flag set to parse help fields or pass that to the commands?
	FormatHelp func([]*Command) string

	// CommandContext optinally specifies a function to set the context for a command. The provided context is derived from the
	// base context and its up to the implementation of the function to wrap or not the returned context with the base context
	// but if not done no cancelation can be provided to the command.
	CommandContext func(context.Context, string) context.Context

	mu       sync.Mutex          // protects the fields below.
	ctx      context.Context     // ctx is the base context used for cancelation.
	cancel   context.CancelFunc  // cancel cancels the base context.
	pr       *PreamptiveReader   // pr is the wrapped preamptive reader. (it is wrapped around R)
	commands map[string]*Command // commands is a mapping of the command name to command.
	running  bool                // indicates if the current dialogue is running.
	close    chan struct{}
}

// Open initialises the dialogue and listens for tokens (provided by the default bufio.Scanner) and maps them to commands.
//
// Open always returns non nil errors. After a call to Shutdown or Close the returned error is context.Canceled.
//
// IMPORTANT:
//
// You can open previously closed dialogues but be aware of the underlaying preamptive reader since it will always be binded to
// the initiall reader and may read messages from the past transaction.
func (d *Dialogue) Open() error {
	d.mu.Lock()
	d.running = true
	d.initLocked()
	d.mu.Unlock()

	scanner := bufio.NewScanner(d.pr)
	for {
		// catch the close signal from the Shutdown call.
		select {
		case <-d.close:
			return d.ctx.Err()
		default:
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

// exit catches any close attempts and returns the context error or returns the provided error.
func (d *Dialogue) exit(err error) error {
	select {
	case <-d.close:
		return d.ctx.Err()
	default:
	}

	d.mu.Lock()
	d.running = false
	d.mu.Unlock()

	return err
}

func (d *Dialogue) initLocked() {
	// check if context doesnt exist or previous context expired (this means the dialogue is being reused).
	if d.ctx == nil || d.ctx.Err() != nil {
		d.ctx, d.cancel = context.WithCancel(context.Background())
	}

	if d.close == nil {
		d.close = make(chan struct{}, 1)
	}

	if d.pr == nil {
		d.pr = NewPreamptiveReader(d.ctx, d.R)
	}
}

func (d *Dialogue) sendCloseNotify() <-chan struct{} {
	notify := make(chan struct{}, 1)

	go func() {
		d.close <- struct{}{}
		notify <- struct{}{}
	}()

	return notify
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

	return command.parseAndRun(cmdCtx, args)
}

// Close imidiately cancels the base context and always returns nil.
func (d *Dialogue) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return nil
	}

	<-d.sendCloseNotify()
	d.cancel()
	d.running = false
	return nil
}

// Shutdown gracefully shuts down the dialogue waiting for the current Read() or Command.Exec() opperation
// without any interuption. It waits indefenetly for the current transaction to finish or till the provided context
// expires. When the context expires the underlaying context is cancelled and the rest of the opperation behaves like a normal
// call to Close().
func (d *Dialogue) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return nil
	}

	notify := d.sendCloseNotify()

	select {
	case <-notify:
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
