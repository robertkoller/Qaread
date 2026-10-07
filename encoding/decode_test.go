package encoding

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"

	goQR "github.com/piglig/go-qr"
)

// Header and trailer only, no QR involved, so a failure here is in the byte layout code
func TestUnwrapBytesRoundtrip(t *testing.T) {
	data := randomBytes(payloadSize, 10)
	name := "a-longer-file-name.txt"
	payload := attachHeader(data, 4, 9000, 12, 0xCAFEBABE, name)

	qr, err := unwrapBytes(payload)
	if err != nil {
		t.Fatalf("unwrapBytes rejected a valid payload: %v", err)
	}
	if qr.id != 0xCAFEBABE {
		t.Errorf("id is %x", qr.id)
	}
	if qr.length != 9000 {
		t.Errorf("length is %d", qr.length)
	}
	if qr.totalChunks != 12 {
		t.Errorf("totalChunks is %d", qr.totalChunks)
	}
	if qr.chunkIndex != 4 {
		t.Errorf("chunkIndex is %d", qr.chunkIndex)
	}
	if trimName(qr.name) != name {
		t.Errorf("name is %q, expected %q", trimName(qr.name), name)
	}
	if !bytes.Equal(qr.data, data) {
		t.Errorf("data doesn't match")
	}
}

func TestUnwrapBytesRejectsCorruption(t *testing.T) {
	payload := attachHeader(randomBytes(200, 11), 0, 200, 1, 1, "x")
	payload[50] ^= 0xFF

	_, err := unwrapBytes(payload)
	if err == nil {
		t.Errorf("a flipped data byte was accepted")
	}
}

// A payload too short to hold the full header but with a valid checksum must error, not panic
func TestUnwrapBytesShortPayloadDoesNotPanic(t *testing.T) {
	for _, size := range []int{17, 20, 30, 41} {
		body := randomBytes(size-4, int64(size))
		trailer := make([]byte, 4)
		binary.BigEndian.PutUint32(trailer, checksum(body))
		payload := append(body, trailer...)

		func() {
			defer func() {
				recovered := recover()
				if recovered != nil {
					t.Errorf("%d byte payload panicked: %v", size, recovered)
				}
			}()
			_, err := unwrapBytes(payload)
			if err == nil {
				t.Errorf("%d byte payload was accepted", size)
			}
		}()
	}
}

// Raw bytes through the QR library, skipping unwrapBytes, so a failure here is the library or rendering
func TestQRByteRoundtrip(t *testing.T) {
	payload := attachHeader(randomBytes(payloadSize, 12), 0, payloadSize, 1, 77, "raw.bin")

	got, err := decodeQRBytesImg(payloadImage(t, payload))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("decoded %d bytes, expected %d identical bytes", len(got), len(payload))
	}
}

func TestDecodeQRImg(t *testing.T) {
	data := randomBytes(payloadSize, 13)
	payload := attachHeader(data, 2, payloadSize*3, 3, 99, "image.bin")

	qr, err := decodeQRImg(payloadImage(t, payload))
	if err != nil {
		t.Fatalf("decodeQRImg failed: %v", err)
	}
	if qr.chunkIndex != 2 || qr.totalChunks != 3 || qr.id != 99 {
		t.Errorf("header fields wrong: %+v", qr)
	}
	if !bytes.Equal(qr.data, data) {
		t.Errorf("data doesn't match")
	}
}

func TestDecodeQRImgRejectsBlankFrame(t *testing.T) {
	blank := image.NewGray(image.Rect(0, 0, 400, 400))
	_, err := decodeQRImg(blank)
	if err == nil {
		t.Errorf("a blank frame decoded without error")
	}
}

// mainEncode to disk then decodeQRPath on every PNG
func TestEncodeDecodeFromDisk(t *testing.T) {
	outputDirectory := t.TempDir()
	data := randomBytes(payloadSize*2+50, 14)

	err := mainEncode(data, "disk.bin", outputDirectory)
	if err != nil {
		t.Fatalf("mainEncode failed: %v", err)
	}

	paths := encodedFramePaths(t, outputDirectory)
	decodedCount := 0
	for _, path := range paths {
		_, err := decodeQRPath(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		decodedCount++
	}
	if decodedCount != len(paths) {
		t.Errorf("decoded %d of %d frames", decodedCount, len(paths))
	}
}

func BenchmarkChecksum(b *testing.B) {
	data := randomBytes(13+maxNameSize+payloadSize, 15)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		checksum(data)
	}
}

func BenchmarkUnwrapBytes(b *testing.B) {
	payload := attachHeader(randomBytes(payloadSize, 16), 0, payloadSize, 1, 1, "bench.bin")
	for b.Loop() {
		unwrapBytes(payload) //nolint:errcheck
	}
}

// The library decode alone, this is the cost the worker pool would parallelize
func BenchmarkQRDecodeLibrary(b *testing.B) {
	payload := attachHeader(randomBytes(payloadSize, 17), 0, payloadSize, 1, 1, "bench.bin")
	frame := payloadImage(b, payload)
	for b.Loop() {
		_, err := goQR.Decode(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeQRImg(b *testing.B) {
	payload := attachHeader(randomBytes(payloadSize, 18), 0, payloadSize, 1, 1, "bench.bin")
	frame := payloadImage(b, payload)
	for b.Loop() {
		decodeQRImg(frame) //nolint:errcheck
	}
}

// Includes reading and PNG decoding the file, compare against BenchmarkDecodeQRImg for the disk overhead
func BenchmarkDecodeQRPath(b *testing.B) {
	outputDirectory := b.TempDir()
	err := mainEncode(randomBytes(payloadSize, 19), "bench.bin", outputDirectory)
	if err != nil {
		b.Fatal(err)
	}
	path := encodedFramePaths(b, outputDirectory)[0]
	for b.Loop() {
		decodeQRPath(path) //nolint:errcheck
	}
}

func BenchmarkBlankFrameRejection(b *testing.B) {
	blank := image.NewGray(image.Rect(0, 0, 1080, 1080))
	for b.Loop() {
		decodeQRImg(blank) //nolint:errcheck
	}
}
