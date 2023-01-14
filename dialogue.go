package dialogue

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
)

type Dialogue struct {
	Prefix          string
	R               io.Reader
	W               io.Writer
	NotFoundHandler func(context.Context, []string) error
	HelpHandler     func([]*Command) string

	ctx    context.Context
	cancel context.CancelFunc

	runningMu sync.Mutex
	running   bool
	done      chan struct{}

	// commands holds all the root commands.
	commands   map[string]*Command
	exchangeWg sync.WaitGroup
}

func NewDialogue(ctx context.Context, prefix string, r io.Reader, w io.Writer, notFound func(context.Context, []string) error) *Dialogue {
	d := &Dialogue{
		Prefix:          prefix,
		R:               r,
		W:               w,
		ctx:             ctx,
		NotFoundHandler: notFound,
		done:            make(chan struct{}),
		commands:        make(map[string]*Command),
	}

	if ctx == nil {
		ctx = context.Background()
	}
	d.ctx, d.cancel = context.WithCancel(ctx)
	d.ctx = context.WithValue(d.ctx, outKey{}, d.W)

	return d
}

// Start starts the main processing thread where values from the dialogue are dispatched
// to their own handlers.
//
// It reports any error returned from the ReadWriter or from parsing the
// dialogue.
func (d *Dialogue) Start() error {
	d.runningMu.Lock()
	d.running = true
	d.runningMu.Unlock()
	defer func() {
		// free the done channel if any closer waiting.
		select {
		case <-d.done:
		default:
		}

		d.runningMu.Lock()
		d.running = false
		d.runningMu.Unlock()
	}()

	scan := bufio.NewScanner(d.R)
	for {
        d.exchangeWg.Add(1)
		tkn, err := d.startExchange(scan)
		if err != nil {
            d.exchangeWg.Add(1)
			return err
		}

		cmd, args, err := parseRawCmd(tkn)
		if err != nil {
			d.exchangeWg.Done()
			return err
		}

		if err := d.dispatchHandler(d.ctx, cmd, args); err != nil {
			d.exchangeWg.Done()
			return err
		}
		d.exchangeWg.Done()
	}
}

// startExchange engages in a new exchange with the reader.
func (d *Dialogue) startExchange(s *bufio.Scanner) ([]string, error) {
	select {
	case <-d.done:
		return nil, io.EOF
	default:
	}

	if _, err := d.W.Write([]byte(d.Prefix)); err != nil {
		return nil, err
	}

	if !s.Scan() {
		return nil, s.Err()
	}

	return strings.Fields(s.Text()), nil
}

// dispatchHandler dispatches the handler for cmd if it exits or the not found handler.
// finally it returns any error from the handlers.
func (d *Dialogue) dispatchHandler(ctx context.Context, cmd string, args []string) error {
	command, ok := d.commands[cmd]
	if !ok {
		tmp := make([]string, 1, len(args)+1)
		tmp[0] = cmd
		copy(tmp[1:], args)

		return d.NotFoundHandler(ctx, tmp)
	}

	return command.parseAndRun(ctx, args)
}

// Stop stops the dialogue asap.
//
// If the dialogue is stuck on processing any value then its stopped immediately.
//
// If the dialogue is stuck on reading / writing to the ReadWriter,
// its stopped as soon as a value goes through.
func (d *Dialogue) Stop() {
	d.runningMu.Lock()
	defer d.runningMu.Unlock()

	if !d.running {
		return
	}

	d.cancel()
	d.done <- struct{}{}
	d.running = false
}

// StopGracefully stops any future dialogue from happening whilst waiting
// for the current dialogue to ensue.
//
// If no current dialogue is happening, the behaviour is the same as with Stop.
func (d *Dialogue) StopGracefully(ctx context.Context) {
	d.runningMu.Lock()
	defer d.runningMu.Unlock()

	if !d.running {
		return
	}

	var wgDone sync.WaitGroup
	wgDone.Add(1)
	go func() { d.done <- struct{}{}; wgDone.Done() }()
	select {
	case <-ctx.Done():
	case <-signalWg(&d.exchangeWg):
	}

	d.cancel()
	wgDone.Wait()
	d.running = false
}

func signalWg(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		wg.Wait()
		ch <- struct{}{}
	}()
	return ch
}

// Handle registers hand as a handler for val. If a handler is already registered
// for val then the old value is replaced.
//
// Handle is no-op after the dialogue started.
func (d *Dialogue) RegisterCommands(cmds ...*Command) {
	d.runningMu.Lock()
	defer d.runningMu.Unlock()

	if d.running {
		return
	}

	for _, c := range cmds {
		d.commands[c.Name] = c
	}
}
