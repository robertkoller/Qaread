package encoding

import (
	"bytes"
	"context"
	"errors"
	"image"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

// receiveConcurrentWithTimeout fails the test instead of hanging if the workers never finish
func receiveConcurrentWithTimeout(t *testing.T, file *File, frames <-chan image.Image) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- file.receiveFramesConcurrent(context.Background(), frames)
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(60 * time.Second):
		t.Fatalf("receiveFramesConcurrent never returned")
		return nil
	}
}

func TestReceiveFramesConcurrent(t *testing.T) {
	data := randomBytes(payloadSize*12+321, 30)
	inOrder := frameImages(t, data, "concurrent.bin", 777)
	blank := image.NewGray(image.Rect(0, 0, 300, 300))

	tests := []struct {
		name   string
		frames []image.Image
	}{
		{"in order", inOrder},
		{"shuffled", shuffled(inOrder, 1)},
		{"looped three times and shuffled", shuffled(append(append(append([]image.Image(nil), inOrder...), inOrder...), inOrder...), 2)},
		{"junk frames mixed in", shuffled(append([]image.Image{blank, blank, blank, blank}, inOrder...), 3)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := &File{}
			err := receiveConcurrentWithTimeout(t, file, sendFrames(test.frames))
			if err != nil {
				t.Fatalf("receiveFramesConcurrent failed: %v", err)
			}

			assembled, err := assembleCompletedFile(file)
			if err != nil {
				t.Fatalf("assembleCompletedFile failed: %v", err)
			}
			if !bytes.Equal(assembled, data) {
				t.Errorf("assembled %d bytes, doesn't match the %d byte original", len(assembled), len(data))
			}
			if file.name != "concurrent.bin" {
				t.Errorf("name is %q", file.name)
			}
		})
	}
}

// With two files in the stream, whichever file's chunk reaches addChunk first wins and the other file's chunks are skipped.
// Which one wins isn't decided by send order, a later frame can finish decoding on another worker first
func TestReceiveFramesConcurrentTwoFilesCompletesOne(t *testing.T) {
	mainData := randomBytes(payloadSize*12+321, 30)
	otherData := randomBytes(payloadSize*2, 31)
	mainFrames := frameImages(t, mainData, "concurrent.bin", 777)
	otherFrames := frameImages(t, otherData, "other.bin", 888)
	mixed := append(mainFrames[:1:1], shuffled(append(append([]image.Image(nil), otherFrames...), mainFrames[1:]...), 4)...)

	file := &File{}
	err := receiveConcurrentWithTimeout(t, file, sendFrames(mixed))
	if err != nil {
		t.Fatalf("receiveFramesConcurrent failed: %v", err)
	}

	assembled, err := assembleCompletedFile(file)
	if err != nil {
		t.Fatalf("assembleCompletedFile failed: %v", err)
	}

	switch file.name {
	case "concurrent.bin":
		if !bytes.Equal(assembled, mainData) {
			t.Errorf("locked onto concurrent.bin but the bytes don't match it")
		}
	case "other.bin":
		if !bytes.Equal(assembled, otherData) {
			t.Errorf("locked onto other.bin but the bytes don't match it")
		}
	default:
		t.Errorf("name is %q", file.name)
	}
}

func TestReceiveFramesConcurrentSingleChunk(t *testing.T) {
	data := randomBytes(100, 32)
	file := &File{}

	err := receiveConcurrentWithTimeout(t, file, sendFrames(frameImages(t, data, "one.bin", 1)))
	if err != nil {
		t.Fatalf("receiveFramesConcurrent failed: %v", err)
	}

	assembled, err := assembleCompletedFile(file)
	if err != nil {
		t.Fatalf("assembleCompletedFile failed: %v", err)
	}
	if !bytes.Equal(assembled, data) {
		t.Errorf("single chunk file doesn't match")
	}
}

func TestReceiveFramesConcurrentMissingChunk(t *testing.T) {
	images := frameImages(t, randomBytes(payloadSize*3, 33), "missing.bin", 2)

	err := receiveConcurrentWithTimeout(t, &File{}, sendFrames(images[:2]))
	if err == nil {
		t.Errorf("reported success with a chunk missing")
	}
}

func TestReceiveFramesConcurrentEmptyChannel(t *testing.T) {
	frames := make(chan image.Image)
	close(frames)

	err := receiveConcurrentWithTimeout(t, &File{}, frames)
	if err == nil {
		t.Errorf("an empty closed channel was treated as success")
	}
}

func TestReceiveFramesConcurrentOnlyJunk(t *testing.T) {
	blank := image.NewGray(image.Rect(0, 0, 300, 300))

	err := receiveConcurrentWithTimeout(t, &File{}, sendFrames([]image.Image{blank, blank, blank}))
	if err == nil {
		t.Errorf("a channel of only junk frames was treated as success")
	}
}

// Every worker should have exited by the time receiveFramesConcurrent returns
func TestReceiveFramesConcurrentNoGoroutineLeak(t *testing.T) {
	images := frameImages(t, randomBytes(payloadSize*5, 34), "leak.bin", 3)
	before := runtime.NumGoroutine()

	err := receiveConcurrentWithTimeout(t, &File{}, sendFrames(images))
	if err != nil {
		t.Fatalf("receiveFramesConcurrent failed: %v", err)
	}

	// goroutine exits aren't instant so give them a moment to settle
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after > before {
		t.Errorf("%d goroutines before, %d after", before, after)
	}
}

// loopFrames acts like the looping video, it never closes the channel and only stops when ctx is cancelled
func loopFrames(ctx context.Context, images []image.Image) <-chan image.Image {
	frames := make(chan image.Image, 4)
	go func() {
		for {
			for _, frame := range images {
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return frames
}

// The file completing has to stop every worker on its own, the channel never closes here
func TestReceiveFramesConcurrentStopsWhenComplete(t *testing.T) {
	data := randomBytes(payloadSize*8+50, 38)
	images := shuffled(frameImages(t, data, "loop.bin", 5), 5)
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	file := &File{}
	result := make(chan error, 1)
	go func() {
		result <- file.receiveFramesConcurrent(ctx, loopFrames(ctx, images))
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("receiveFramesConcurrent failed: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("receiveFramesConcurrent never returned with a producer that never closes")
	}
	cancel()

	assembled, err := assembleCompletedFile(file)
	if err != nil {
		t.Fatalf("assembleCompletedFile failed: %v", err)
	}
	if !bytes.Equal(assembled, data) {
		t.Errorf("assembled bytes don't match")
	}

	// the workers and the producer should all be gone once ctx is cancelled
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after > before {
		t.Errorf("%d goroutines before, %d after", before, after)
	}
}

// Cancelling from outside should stop a receive that will never finish, here the producer only ever sends one of three chunks
func TestReceiveFramesConcurrentCallerCancel(t *testing.T) {
	images := frameImages(t, randomBytes(payloadSize*3, 39), "cancel.bin", 6)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- (&File{}).receiveFramesConcurrent(ctx, loopFrames(ctx, images[:1]))
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected a context.Canceled error, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("receiveFramesConcurrent ignored the cancel")
	}
}

// Hammers addChunk from many goroutines with no images involved so -race can see the locking directly
func TestAddChunkConcurrentCalls(t *testing.T) {
	chunkCount := 1000
	data := randomBytes(payloadSize*chunkCount, 35)

	var chunks []QR
	for index, payload := range headedChunks(data, "hammer.bin", 42) {
		qr, err := unwrapBytes(payload)
		if err != nil {
			t.Fatalf("chunk %d: %v", index, err)
		}
		chunks = append(chunks, qr)
	}

	file := &File{}
	workerCount := 16
	doneReports := make(chan struct{}, workerCount*chunkCount)
	var waitGroup sync.WaitGroup

	// every worker adds every chunk so each one arrives 16 times
	for worker := range workerCount {
		waitGroup.Go(func() {
			for _, qr := range shuffledQRs(chunks, int64(worker)) {
				done, err := file.addChunk(qr)
				if err != nil {
					t.Errorf("addChunk rejected a valid chunk: %v", err)
				}
				if done {
					doneReports <- struct{}{}
				}
			}
		})
	}
	waitGroup.Wait()
	close(doneReports)

	if int(file.count) != chunkCount {
		t.Errorf("count is %d, expected %d", file.count, chunkCount)
	}
	if len(doneReports) != 1 {
		t.Errorf("done was reported %d times, expected exactly once", len(doneReports))
	}

	assembled, err := assembleCompletedFile(file)
	if err != nil {
		t.Fatalf("assembleCompletedFile failed: %v", err)
	}
	if !bytes.Equal(assembled, data) {
		t.Errorf("assembled bytes don't match")
	}
}

func TestAddChunkRejectsBadChunks(t *testing.T) {
	payloads := headedChunks(randomBytes(payloadSize*3, 36), "reject.bin", 50)
	first, _ := unwrapBytes(payloads[0])
	file := &File{}

	_, err := file.addChunk(first)
	if err != nil {
		t.Fatalf("first chunk rejected: %v", err)
	}

	wrongFile := first
	wrongFile.id = 51
	_, err = file.addChunk(wrongFile)
	if err == nil {
		t.Errorf("a chunk with a different file id was accepted")
	}

	outOfRange := first
	outOfRange.chunkIndex = 3
	_, err = file.addChunk(outOfRange)
	if err == nil {
		t.Errorf("an out of range chunk index was accepted")
	}

	done, err := file.addChunk(first)
	if err != nil || done {
		t.Errorf("a duplicate returned done=%v err=%v", done, err)
	}
	if file.count != 1 {
		t.Errorf("count is %d after one real chunk and a duplicate", file.count)
	}
}

func shuffledQRs(chunks []QR, seed int64) []QR {
	copied := append([]QR(nil), chunks...)
	generator := rand.New(rand.NewSource(seed))
	generator.Shuffle(len(copied), func(first, second int) {
		copied[first], copied[second] = copied[second], copied[first]
	})
	return copied
}

// Same 100 chunk file through both receivers so the numbers compare directly
func benchmarkReceiver(b *testing.B, receive func(file *File, frames <-chan image.Image) error) {
	chunkCount := 100
	images := frameImages(b, randomBytes(payloadSize*chunkCount, 37), "bench.bin", 4)
	for b.Loop() {
		err := receive(&File{}, sendFrames(images))
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*chunkCount)/1e6, "ms/chunk")
}

func BenchmarkReceive100ChunksSequential(b *testing.B) {
	benchmarkReceiver(b, (*File).receiveFrames)
}

func BenchmarkReceive100ChunksConcurrent(b *testing.B) {
	benchmarkReceiver(b, func(file *File, frames <-chan image.Image) error {
		return file.receiveFramesConcurrent(context.Background(), frames)
	})
}
