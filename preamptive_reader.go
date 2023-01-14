package dialogue

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
)

// NewPreamptiveReader creates a new preamptive reader whith the provided context.
//
// If the context gets cancelled the caller waiting for the input will return early.
//
// IMPORTANT: the blocking read wont magically get cleaned up and will continue running till the read closes. You can still read from the
// buffer which was allocated by the initiall call to Read (which got cancelled by the context) till the buffer is empty, calls to read after
// the allocated buffer was emptied will all result in io.EOF .
func NewPreamptiveReader(ctx context.Context, r io.Reader) *PreamptiveReader {
    pr := &PreamptiveReader{
        r: r,
        ctx: ctx,
        writeTo: make(chan []byte),
        readFrom: make(chan int, 1),
    }

    go pr.listen()

    return pr
}

// wrap around io.Reader for a work around read cancellations.
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

    buf []byte // refference to the current buffer provided by the call to Read().
    writeTo chan []byte // tells the reader we want to read.
    readFrom chan int // signals that the request read is over to whoever is intereseted.
    err error // sticky error.
}

// listen reads from r on demand.
func (r *PreamptiveReader) listen() {
    for {
        select {
        case <-r.ctx.Done():
            r.err = r.ctx.Err()
            return
        case buf := <-r.writeTo: // commit to a read.
            r.buf = buf // keep a refference to the buffer.
            n, err := r.r.Read(buf)
            if err != nil {
                r.err = err
                close(r.readFrom)
                return
            }

            r.readFrom <- n
        }
    }
}

// Read reads requests a read from the preamptive reader. It behaves normally but returns early with n = 0 and the context error
// when the context gets cancelled.
//
// The listen go routine wont get cleaned up till the stranded read returns, up until the cleanup, the first registered read will claim the
// stranded read and work as expected.
func (r *PreamptiveReader) Read(buf []byte) (int, error) {
    fresh, err := r.claim()
    if err != nil {
        return 0, err
    }
    defer r.claimRead.Store(false)

    if fresh {
        r.writeTo <- buf

        select {
            case <-r.ctx.Done():
                return 0, r.ctx.Err()
            case n, ok := <-r.readFrom:
                if !ok {
                    return 0, r.err
                }

                return n, nil
        }
    }

    // FASTPATH: dont bother copying any values to a zero lenght buffer.
    if len(buf) == 0 {
        return 0, nil
    }

    // the read isnt fresh.
    n, ok := <- r.readFrom
    if !ok {
        return 0, r.err
    }

    // copy values from previous buffer (provided by last read call) to current buffer
    // while maintaining the readFrom state.
    nn := copy(buf, r.buf[:n])

    n -= nn
    // last read, no more data left in the buffer.
    if n == 0 {
        close(r.readFrom)
        r.err = io.EOF
        return nn, nil
    }

    r.readFrom <- n
    return nn, nil 
}

// claim claims the preamptive reader and indicates if the claimed reader is new.
func (r *PreamptiveReader) claim() (bool, error) {
    if !r.claimRead.CompareAndSwap(false, true) {
        return false, errors.New("cannot claim a read in process")
    }

    select {
        case <-r.ctx.Done():
            return false, nil
        default:
            return true, nil
    }
}
