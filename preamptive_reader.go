package dialogue

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
)

// NewPreamptiveReader creates a new preamptive reader with the provided context used to inidcate cancelation. The created
// preamptive reader wraps and re directs reads to r.
func NewPreamptiveReader(ctx context.Context, r io.Reader) *PreamptiveReader {
	pr := &PreamptiveReader{
		r:             r,
		ctx:           ctx,
		writeFromMem:  make(chan []byte),
		writeFromRead: make(chan []byte),
		bytesRead:     make(chan int),
	}

	go pr.listen()

	return pr
}

// PreamptiveReader is a wrapper around r which provides cancellable reads. Reads are 1:1 up untill the context gets cancelled.
// Calls to read after the context cancellation can block untill the previous read opperation is over. Once the read is over 
// the previous buffer provided by the cancelled read call will be consumed on each subsequent read call till its all consumed and 
// io.EOF is returned.
//
// IMPORTANT:
//
// Concurrent read calls will return an error.
//
// The read call to the source reader isnt cancelled itself, only the wrapped Read call returns early, the 
// source read wont get cleaned up unitl the previous buffer is consumed.
//
// inspired by https://benjamincongdon.me/blog/2020/04/23/Cancelable-Reads-in-Go/
type PreamptiveReader struct {
	// the source reader.
	r io.Reader

	// the context used to signal cancellations.
	ctx context.Context

	// claimRead signals wether the current read can be claimed or is already claimed.
	//
	// this makes the process synchronous, one reader at a time, the rest will return an error.
	claimRead atomic.Bool

	buf           []byte      // refference to the current buffer provided by the call to Read().
	writeFromMem  chan []byte // reads from buf.
	writeFromRead chan []byte // reads from the reader.
	bytesRead     chan int    // signals that the request read is over to whoever is intereseted.
	err           error       // sticky error.
}

// listen reads from r or buf on demand.
func (r *PreamptiveReader) listen() {
	var n int
	var err error

	for {
		select {
		case buf := <-r.writeFromMem: // a read request on a closed context, try to read from the remaining buffer, this is quick.
			n, err = r.readFromBuf(buf)
		case buf := <-r.writeFromRead: // commit to a new to read.
			n, err = r.r.Read(buf)
			r.buf = buf
		}

		if err != nil {
			r.err = err
			close(r.bytesRead)
			return
		}

		select {
		case buf := <-r.writeFromMem: // got read from mem request druing the read call.
			n, _ := r.readFromBuf(buf)
			r.bytesRead <- n

		case r.bytesRead <- n:
		}
	}
}

// readFromBuf copies the accumulated r.buf into buf and truncates r.buf. If len(r.buf) == 0 io.EOF is returned.
func (r *PreamptiveReader) readFromBuf(buf []byte) (int, error) {
	if len(r.buf) == 0 {
		return 0, io.EOF
	}

	n := copy(buf, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// Read reads requests a read from the preamptive reader. It behaves normally but returns early with n = 0 and the context error
// when the context gets cancelled.
//
// The listen go routine wont get cleaned up till the stranded read returns, up until the cleanup, the first registered read will claim the
// stranded read and work as expected.
func (r *PreamptiveReader) Read(buf []byte) (int, error) {
	if !r.claimRead.CompareAndSwap(false, true) {
		return 0, errors.New("cannot claim a read during another read")
	}
	defer r.claimRead.Store(false)

	// check wether we are calling after a cancelled context or before.
	select {
	case <-r.ctx.Done(): // call after cancelled context. Read only from memory, this opperation is usually fast excluding the times when we wait for an ongoing read.
		// FASTPATH: buffer size is 0 and read is from memory, return early.
		if len(buf) == 0 {
			return 0, nil
		}

		r.writeFromMem <- buf

		n, ok := <-r.bytesRead
		if !ok {
			return 0, r.err
		}

		return n, nil
	default:
	}

	// context not cancelled and we claimed the reader. We need to request a new read.
	r.writeFromRead <- buf

	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case n, ok := <-r.bytesRead:
		if !ok {
			return 0, r.err
		}

		return n, nil
	}
}
