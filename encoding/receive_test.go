package encoding

import (
	"bytes"
	"image"
	"math/rand"
	"testing"
	"time"
)

// receiveWithTimeout runs receiveFrames but fails the test instead of hanging if it never returns
func receiveWithTimeout(t *testing.T, file *File, frames <-chan image.Image) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- file.receiveFrames(frames)
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(60 * time.Second):
		t.Fatalf("receiveFrames never returned")
		return nil
	}
}

func shuffled(images []image.Image, seed int64) []image.Image {
	generator := rand.New(rand.NewSource(seed))
	copied := append([]image.Image(nil), images...)
	generator.Shuffle(len(copied), func(first, second int) {
		copied[first], copied[second] = copied[second], copied[first]
	})
	return copied
}

func TestReceiveFrames(t *testing.T) {
	data := randomBytes(payloadSize*4+321, 20)
	inOrder := frameImages(t, data, "receive.bin", 555)
	blank := image.NewGray(image.Rect(0, 0, 300, 300))

	tests := []struct {
		name   string
		frames []image.Image
	}{
		{"in order", inOrder},
		{"shuffled", shuffled(inOrder, 1)},
		{"looped twice and shuffled", shuffled(append(append([]image.Image(nil), inOrder...), inOrder...), 2)},
		{"junk frames mixed in", shuffled(append([]image.Image{blank, blank, blank}, inOrder...), 3)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := &File{}
			err := receiveWithTimeout(t, file, sendFrames(test.frames))
			if err != nil {
				t.Fatalf("receiveFrames failed: %v", err)
			}

			assembled, err := assembleCompletedFile(file)
			if err != nil {
				t.Fatalf("assembleCompletedFile failed: %v", err)
			}
			if !bytes.Equal(assembled, data) {
				t.Errorf("assembled %d bytes, doesn't match the %d byte original", len(assembled), len(data))
			}
			if trimName(file.name) != "receive.bin" {
				t.Errorf("name is %q", trimName(file.name))
			}
		})
	}
}

func TestReceiveFramesSingleChunk(t *testing.T) {
	data := randomBytes(100, 21)
	file := &File{}

	err := receiveWithTimeout(t, file, sendFrames(frameImages(t, data, "one.bin", 1)))
	if err != nil {
		t.Fatalf("receiveFrames failed: %v", err)
	}

	assembled, err := assembleCompletedFile(file)
	if err != nil {
		t.Fatalf("assembleCompletedFile failed: %v", err)
	}
	if !bytes.Equal(assembled, data) {
		t.Errorf("single chunk file doesn't match")
	}
}

func TestReceiveFramesMissingChunk(t *testing.T) {
	images := frameImages(t, randomBytes(payloadSize*3, 22), "missing.bin", 2)
	file := &File{}

	err := receiveWithTimeout(t, file, sendFrames(images[:2]))
	if err == nil {
		t.Errorf("receiveFrames reported success with a chunk missing")
	}
}

func TestReceiveFramesWrongFileID(t *testing.T) {
	first := frameImages(t, randomBytes(payloadSize*2, 23), "first.bin", 100)
	other := frameImages(t, randomBytes(payloadSize, 24), "other.bin", 200)
	file := &File{}

	// one chunk of the right file then a chunk of a different file, the current design aborts on this
	err := receiveWithTimeout(t, file, sendFrames([]image.Image{first[0], other[0], first[1]}))
	if err == nil {
		t.Errorf("a chunk from another file was not reported")
	}
}

func TestReceiveFramesEmptyChannel(t *testing.T) {
	frames := make(chan image.Image)
	close(frames)

	err := (&File{}).receiveFrames(frames)
	if err == nil {
		t.Errorf("an empty closed channel was treated as success")
	}
}

func TestAssembleCompletedFile(t *testing.T) {
	chunks := chunker(randomBytes(payloadSize*2+5, 25))
	complete := &File{frames: chunks, totalChunks: 3, count: 3}

	assembled, err := assembleCompletedFile(complete)
	if err != nil {
		t.Fatalf("complete file rejected: %v", err)
	}
	if !bytes.Equal(assembled, bytes.Join(chunks, nil)) {
		t.Errorf("assembled bytes don't match")
	}

	incomplete := &File{frames: [][]byte{chunks[0], nil, chunks[2]}, totalChunks: 3, count: 2}
	_, err = assembleCompletedFile(incomplete)
	if err == nil {
		t.Errorf("incomplete file was assembled")
	}
}

// Whole receive path for a 20 chunk file (about 16 KB) fed as images, one goroutine decoding
func BenchmarkReceiveFrames20Chunks(b *testing.B) {
	chunkCount := 20
	images := frameImages(b, randomBytes(payloadSize*chunkCount, 26), "bench.bin", 3)
	for b.Loop() {
		file := &File{}
		file.receiveFrames(sendFrames(images)) //nolint:errcheck
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*chunkCount)/1e6, "ms/chunk")
}
