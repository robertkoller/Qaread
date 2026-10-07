package encoding

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
)

// receiveFramesConcurrent is the worker pool version of receiveFrames, several workers decode frames at once and share the File through its lock.
// It returns once the file is complete, the frames channel closes, or ctx is cancelled. The producer should watch the same ctx and the caller
// should cancel it after this returns so a looping producer stops sending
func (file *File) receiveFramesConcurrent(ctx context.Context, frames <-chan image.Image) error {
	// workers cancel this when the file completes, which wakes up the ones still waiting on frames
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := runtime.NumCPU()
	wg := sync.WaitGroup{}

	for range workers {
		wg.Go(func() {
			file.decodeWorker(workerContext, cancel, frames)
		})
	}

	wg.Wait()

	if !file.started {
		if ctx.Err() != nil {
			return fmt.Errorf("receive cancelled before any chunk arrived: %w", ctx.Err())
		}
		return errors.New("File was never started, input probably junk")
	}

	if file.count != file.totalChunks {
		if ctx.Err() != nil {
			return fmt.Errorf("receive cancelled with %d of %d chunks: %w", file.count, file.totalChunks, ctx.Err())
		}
		return errors.New("Not all chunks are finished")
	}

	return nil
}

// decodeWorker pulls frames off the shared channel until it closes, ctx is cancelled, or this worker adds the last chunk.
// It runs as a goroutine so it has no return value, anything it returned would be thrown away
func (file *File) decodeWorker(ctx context.Context, cancel context.CancelFunc, frames <-chan image.Image) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}

			currQR, err := decodeQRImg(frame)
			if err != nil {
				continue
			}

			// a chunk addChunk rejects is just a bad frame like a failed decode, the looping video still brings the good ones
			done, err := file.addChunk(currQR)
			if err != nil {
				continue
			}

			if done {
				cancel()
				return
			}
		}
	}
}

// addChunk is the only place that reads or writes file state while workers are running
func (file *File) addChunk(currQR QR) (bool, error) {
	file.lock.Lock()
	defer file.lock.Unlock()

	// the first chunk only sets the file up, it gets stored and counted by the same code as every other chunk below
	if !file.started {
		file.id = currQR.id
		file.totalChunks = currQR.totalChunks
		file.frames = make([][]byte, currQR.totalChunks)
		file.started = true
		file.name = currQR.name
	}

	if currQR.id != file.id {
		return false, errors.New("ID of QR does not match downloading file ID")
	}

	if currQR.chunkIndex >= file.totalChunks {
		return false, errors.New("chunk index out of bounds of total chunks, some error somewhere")
	}

	if file.frames[currQR.chunkIndex] != nil {
		return false, nil
	}

	file.frames[currQR.chunkIndex] = currQR.data
	file.count++

	return file.count == file.totalChunks, nil
}
