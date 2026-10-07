package encoding

import (
	"bytes"
	"image"
	"math/rand"
	"testing"

	goQR "github.com/piglig/go-qr"
)

// randomBytes gives deterministic test data so failures are reproducible
func randomBytes(size int, seed int64) []byte {
	generator := rand.New(rand.NewSource(seed))
	data := make([]byte, size)
	generator.Read(data)
	return data
}

// headedChunks builds the exact payloads mainEncode would put in each QR code
func headedChunks(data []byte, name string, id uint32) [][]byte {
	chunked := chunker(data)
	length := uint32(len(data))
	totalChunks := uint16(len(chunked))

	var payloads [][]byte
	for index, chunk := range chunked {
		payloads = append(payloads, attachHeader(chunk, index, length, totalChunks, id, name))
	}
	return payloads
}

// payloadImage renders one payload as a QR image in memory with the same settings mainEncode uses
func payloadImage(t testing.TB, payload []byte) image.Image {
	t.Helper()
	code, err := goQR.EncodeBinary(payload, goQR.High)
	if err != nil {
		t.Fatalf("EncodeBinary failed: %v", err)
	}
	return renderFrame(code, frameScale, frameBorder)
}

// frameImages renders every chunk of data as a QR image, in chunk order
func frameImages(t testing.TB, data []byte, name string, id uint32) []image.Image {
	t.Helper()
	var images []image.Image
	for _, payload := range headedChunks(data, name, id) {
		images = append(images, payloadImage(t, payload))
	}
	return images
}

// sendFrames feeds images into a channel from a goroutine and closes it when done, like a producer would
func sendFrames(images []image.Image) <-chan image.Image {
	frames := make(chan image.Image, 4)
	go func() {
		defer close(frames)
		for _, frame := range images {
			frames <- frame
		}
	}()
	return frames
}

// trimName drops the zero padding the fixed width name field leaves behind
func trimName(name string) string {
	return string(bytes.TrimRight([]byte(name), "\x00"))
}
