package dialogue

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestShutdownWaitCurrentTransaction(t *testing.T) {
	t.Parallel()

	msg := "transaction complete"
	w := newWriteExpected(t, []byte(msg))

	d := &Dialogue{
		R: stallingReader{
			r: strings.NewReader("idk what im typing here\n"),
			d: 5 * time.Second,
		},
		W: w,
		CommandNotFound: func(ctx context.Context, args []string) error {
			_, err := w.Write([]byte(msg))
			return err
		},
	}
	d.RegisterCommands(testCommand)

	closed := notifyClose(d)
	time.Sleep(1 * time.Second)
	go d.Shutdown(context.Background())
	timeStart := time.Now()

	select {
	case err := <-closed:
		timeElapsed := time.Now().Sub(timeStart)
		if timeElapsed < 4*time.Second {
			t.Fatal("expected to close after 4 seconds")
		}

		if err != ErrDialogueClosed {
			t.Fatalf("expected error to be %v but got %v", ErrDialogueClosed, err)
		}

		// get the underlaying reader to check if there was only one call to read.
		pr := d.PreamptiveReader()
		if _, err := pr.Read(make([]byte, 1)); err != io.EOF { // use buffer w capacity 1 to advance the reader, preamptive reader ignores memory reads with buffer sizes = 0.
			t.Fatal("expected only one call to read")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("expected to close before 6 seconds")
	}
}

func TestShutdownDeadline(t *testing.T) {
	t.Parallel()

	msg := []byte("I hate unit tests")

	d := &Dialogue{
		R: stallingReader{
			r: bytes.NewReader(msg),
			d: 5 * time.Second,
		},
		W: nopReadWriter{},
		CommandNotFound: func(ctx context.Context, args []string) error {
			t.Fatal("command not expected to run")
			return nil
		},
	}
	d.RegisterCommands(testCommand)

	closed := notifyClose(d)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	time.Sleep(1 * time.Second)
	go d.Shutdown(ctx)

	select {
	case err := <-closed:
		if err != ErrDialogueClosed {
			t.Fatalf("expected error to be %v but got %v", ErrDialogueClosed, err)
		}

		// take over the failed read to confirm it happened.
		buf := make([]byte, len(msg))
		pr := d.PreamptiveReader()

		_, err = pr.Read(buf)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(buf, msg) {
			t.Fatalf("got unexpected read: %s != %s", buf, msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("expected to close before 4 seconds")
	}
}

func TestHelpCommand(t *testing.T) {
	t.Run("focus", func(t *testing.T) {
		// expect the output to be the focused format of the command.
		w := newWriteExpected(t, []byte(testCommand.FormatHelp(
			testCommand,
			true,
		)))

		d := &Dialogue{
			R:       strings.NewReader("help -n test\nquit\n"),
			W:       w,
			HelpCmd: "help",
			QuitCmd: "quit",
		}
		d.RegisterCommands(testCommand)

		if err := d.Open(); err != ErrDialogueClosed {
			t.Fatalf("got unexpected error: %v", err)
		}

		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("generic", func(t *testing.T) {
		d := &Dialogue{
			R:       strings.NewReader("help\nquit\n"),
			HelpCmd: "help",
			QuitCmd: "quit",
		}
		d.RegisterCommands(testCommand)

		// initialise commands to access the FormatHelp methods.
		d.init()

		w := newWriteExpected(t, []byte(d.FormatHelp("", d.commands)))
		d.W = w

		if err := d.Open(); err != ErrDialogueClosed {
			t.Fatalf("recieved unexpected err: %v", err)
		}

		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDefaultCommandNotFound(t *testing.T) {
	d := Dialogue{
		R:       strings.NewReader("this command doesnt exits\ntest\nquit\n"),
		QuitCmd: "quit",
	}
	d.RegisterCommands(testCommand)

	d.init()

	expected := "Command: this not found\n" + d.FormatHelp("", d.commands)
	w := newWriteExpected(t, []byte(expected))
	d.W = w

	if err := d.Open(); err != ErrDialogueClosed {
		t.Fatalf("recieved unexpected err: %v", err)
	}

	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultCommandQuit(t *testing.T) {
}

func TestReadAfterClose(t *testing.T) {

}

func notifyClose(d *Dialogue) <-chan error {
	errC := make(chan error, 1)

	go func() {
		err := d.Open()
		errC <- err
	}()

	return errC
}

type nopReadWriter struct{}

func (rw nopReadWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (rw nopReadWriter) Read(buf []byte) (int, error) {
	return len(buf), nil
}

type writeExpected struct {
	expected []byte
	wX       int
	t        *testing.T
}

func newWriteExpected(t *testing.T, buf []byte) *writeExpected {
	return &writeExpected{
		expected: buf,
		t:        t,
	}
}

func (w *writeExpected) Write(p []byte) (int, error) {
	wn := len(p)

	if wn > len(w.expected[w.wX:]) {
		err := fmt.Errorf("writeExpected: input buffer to long: %q", p)
		w.t.Error(err.Error())
		return 0, err
	}

	for i, b := range p {
		expectedB := w.expected[w.wX+i]
		if b != expectedB {
			err := fmt.Errorf("writeExpected: missmatched bytes at index %d %v != %v with buffer: %q", w.wX+i, expectedB, b, p)
			w.t.Error(err.Error())

			// the strings matched till i.
			return i, err
		}
	}

	w.wX += wn
	return wn, nil
}

func (w *writeExpected) Flush() error {
	if w.wX != len(w.expected) {
		err := fmt.Errorf("writeExpected: there are still bytes to be written")
		w.t.Error(err.Error())
		return err
	}

	return nil
}
