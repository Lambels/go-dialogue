package dialogue

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// TestReadAll tests wether the preamptive reader can read normally through opperations like io.ReadAll.
func TestReadAll(t *testing.T) {
	r := strings.NewReader("Testing")
	ctx := context.Background()

	pr := NewPreamptiveReader(ctx, r)
	out, err := io.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != "Testing" {
		t.Fatalf("expected: Testing but got %s", out)
	}
}

// TestReadAfterCancel tests wether the preamptive reader returns an io.EOF when called with a cancelled context
// and no extra state.
func TestReadAfterCancel(t *testing.T) {
	r := strings.NewReader("Testing")
	ctx, cancel := context.WithCancel(context.Background())

	pr := NewPreamptiveReader(ctx, r)
	buf := make([]byte, 7)

	if _, err := pr.Read(buf[:3]); err != nil {
		t.Fatal(err)
	}

	cancel()

	if n, err := pr.Read(buf[3:]); err != io.EOF {
		t.Fatalf("expected EOF error but got: %v and buf: %v", err, buf[n:])
	}
}

// TestReadDuringCancel tests wether the preamptive reader can take over a context read and handle the internal
// state.
func TestReadDuringCancel(t *testing.T) {
	r := stallingReader{
		r: strings.NewReader("Testing"),
		d: 1 * time.Second,
	}

	// make sure that the first call to read will allocate a buffered read but return early.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	pr := NewPreamptiveReader(ctx, r)
	if _, err := pr.Read(make([]byte, 7)); err != context.DeadlineExceeded { // call the underlaying read with an allocated buffer of 7 bytes.
		t.Fatalf("expected EOF error but got: %v", err)
	}

	// read from the underlaying buffer 1 byte at a time, this call should work for exactly 7 times till the
	// underlaying buffer (allocated in the first call to read) will be consumed.
	buf := make([]byte, 1)
	for i := 0; i < 7; i++ {
		if _, err := pr.Read(buf); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := pr.Read(buf); err != io.EOF {
		t.Fatalf("expected EOF error but got: %v and bu: %v", err, buf[n:])
	}
}

// stallingReader simulates an uncancellable reader which sleeps for d time and redirects the reads to the
// wrapped reader.
type stallingReader struct {
	r io.Reader

	d time.Duration
}

func (r stallingReader) Read(buf []byte) (int, error) {
	time.Sleep(r.d)

	return r.r.Read(buf)
}
