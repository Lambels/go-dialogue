package dialogue_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Lambels/go-dialogue"
)

func TestShutdownWaitCurrentTransaction(t *testing.T) {
    t.Parallel()

	msg := "transaction complete"
	w := NewWriteExpected(t, []byte(msg))

	d := &dialogue.Dialogue{
		R: stallingReader{
			r: strings.NewReader("idk what im typing here"),
			d: 5 * time.Second,
		},
		W: w,
		CommandNotFound: func(ctx context.Context, args []string) error {
            _, err := w.Write([]byte(msg))
			return err
		},
	}

    closed := notifyClose(d)
    go d.Shutdown(context.Background())
    timeStart := time.Now()

    select {
    case err := <-closed:
        timeElapsed := timeStart.Sub(time.Now())
        if timeElapsed < 4 * time.Second {
            t.Fatal("expected dialogue to close after 4 seconds")
        }

        if err != dialogue.ErrDialogueClosed {
            t.Fatalf("expected error to be %v but got %v", dialogue.ErrDialogueClosed, err)
        }

        // get the underlaying reader to check if there was only one call to read.
        pr := d.PreamptiveReader()
        if _, err := pr.Read(make([]byte, 1)); err != io.EOF { // use buffer w capacity 1 to advance the reader, preamptive reader ignores memory reads with buffer sizes = 0.
            t.Fatal("expected only one call to read")
        }

    case <-time.After(6 * time.Second):
        t.Fatal("expected dialogue to close before 6 seconds")
    }
}

func TestShutdownDeadline(t *testing.T) {
    t.Parallel()

    msg := []byte("I hate unit tests")

    d := &dialogue.Dialogue{
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

    closed := notifyClose(d)
    ctx, cancel := context.WithTimeout(context.Background(), 3 * time.Second)
    defer cancel()

    go d.Shutdown(ctx)

    select {
    case err := <-closed:
        if err != dialogue.ErrDialogueClosed {
            t.Fatalf("expected error to be %v but got %v", dialogue.ErrDialogueClosed, err)
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
        t.Fatal("expected dialogue to close before 4 seconds")
    }
}

func TestClose(t *testing.T) {
}

func TestHelpCommand(t *testing.T) {

}

func TestDefaultCommandNotFound(t *testing.T) {
}

func TestReadAfterClose(t *testing.T) {

}

func notifyClose(d *dialogue.Dialogue) <-chan error {
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

func NewWriteExpected(t *testing.T, buf []byte) *writeExpected {
	return &writeExpected{
		expected: buf,
		t:        t,
	}
}

func (w *writeExpected) Write(p []byte) (int, error) {
	wn := len(p)

	if wn > len(w.expected[w.wX:]) {
		err := fmt.Errorf("writeExpected: input buffer to long")
		w.t.Error(err.Error())
		return 0, err
	}

	for i, b := range p {
		expectedB := w.expected[w.wX+i]
		if b != expectedB {
			err := fmt.Errorf("writeExpected: missmatched bytes at index %d %s != %s", w.wX+i, expectedB, b)
			w.t.Error(err.Error())

			// the strings matched till i.
			return i, err
		}
	}

	w.wX += wn
	return wn, nil
}
