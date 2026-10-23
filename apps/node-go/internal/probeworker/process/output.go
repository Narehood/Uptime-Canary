package process

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

const outputPipeGrace = 10 * time.Millisecond

// Output pipes belong to us, not exec's copying goroutines. Process.Wait must
// not discard readable output just because its consumer was not scheduled.
type outputCollector struct {
	file        *os.File
	sink        *streamSink
	done        chan struct{}
	read        string // written by collect; read only after done
	eof         bool
	probes      int // parent only
	heldOpen    bool
	probeFailed bool
}

func collectOutput(file *os.File, sink *streamSink, writer io.Writer) *outputCollector {
	c := &outputCollector{file: file, sink: sink, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		var b [4096]byte
		for {
			n, err := file.Read(b[:])
			if n > 0 {
				written, writeErr := writer.Write(b[:n])
				if writeErr != nil || written != n {
					c.read = "write-error"
					return
				}
			}
			if err != nil {
				switch {
				case errors.Is(err, io.EOF):
					c.eof, c.read = true, "eof"
					if sink.finish() != nil {
						c.read = "rejected"
					}
				case errors.Is(err, os.ErrClosed):
					c.read = "closed"
				default:
					c.read = "read-error"
				}
				return
			}
		}
	}()
	return c
}

// awaitOutput is called after the direct child has been reaped. The 10 ms
// grace still bounds an empty pipe held open by another writer. Readable data
// or kernel EOF is allowed to drain under the original total context deadline.
// Readiness is inspected without consuming data or using another I/O goroutine.
func awaitOutput(ctx context.Context, out, errout *outputCollector) bool {
	timer := time.NewTimer(outputPipeGrace)
	defer timer.Stop()
	outDone, errDone := out.done, errout.done
	abort := func() bool {
		_ = out.file.Close()
		_ = errout.file.Close()
		<-out.done
		<-errout.done
		return false
	}
	for outDone != nil || errDone != nil {
		select {
		case <-outDone:
			outDone = nil
		case <-errDone:
			errDone = nil
		case <-ctx.Done():
			return abort()
		case <-timer.C:
			for _, c := range []*outputCollector{out, errout} {
				select {
				case <-c.done:
					continue
				default:
				}
				c.probes++
				ready, err := pipeReadable(c.file)
				if err != nil || !ready {
					c.heldOpen, c.probeFailed = !ready && err == nil, err != nil
					return abort()
				}
			}
			timer.Reset(outputPipeGrace)
		}
	}
	return out.eof && errout.eof && out.read == "eof" && errout.read == "eof"
}
