package encoding

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestChunker(t *testing.T) {
	tests := []struct {
		name           string
		size           int
		expectedChunks int
		lastChunkSize  int
	}{
		{"empty", 0, 0, 0},
		{"one byte", 1, 1, 1},
		{"exactly one chunk", payloadSize, 1, payloadSize},
		{"one byte over", payloadSize + 1, 2, 1},
		{"exact multiple", payloadSize * 5, 5, payloadSize},
		{"uneven", payloadSize*3 + 123, 4, 123},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := randomBytes(test.size, 1)
			chunks := chunker(data)

			if len(chunks) != test.expectedChunks {
				t.Fatalf("got %d chunks, expected %d", len(chunks), test.expectedChunks)
			}
			if test.expectedChunks == 0 {
				return
			}
			if len(chunks[len(chunks)-1]) != test.lastChunkSize {
				t.Errorf("last chunk is %d bytes, expected %d", len(chunks[len(chunks)-1]), test.lastChunkSize)
			}
			if !bytes.Equal(bytes.Join(chunks, nil), data) {
				t.Errorf("joined chunks don't match the input")
			}
		})
	}
}

func TestCreateNameBuffer(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"short", "file.txt", "file.txt"},
		{"exactly max", "abcdefghijklmnopqrstuvwxy", "abcdefghijklmnopqrstuvwxy"},
		{"too long", "abcdefghijklmnopqrstuvwxyz0123", "abcdefghijklmnopqrstuvwxy"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := string(createNameBuffer(test.input, maxNameSize))
			if got != test.expected {
				t.Errorf("got %q, expected %q", got, test.expected)
			}
		})
	}
}

// A multi byte character straddling the cutoff shouldn't leave a broken half character behind
func TestCreateNameBufferUTF8Cutoff(t *testing.T) {
	// 24 ASCII bytes then "é" (2 bytes) puts the cut in the middle of the é
	input := "abcdefghijklmnopqrstuvwx" + "é" + "z"
	got := createNameBuffer(input, maxNameSize)

	if len(got) > maxNameSize {
		t.Fatalf("buffer is %d bytes, max is %d", len(got), maxNameSize)
	}
	if !bytes.Equal(got, []byte("abcdefghijklmnopqrstuvwx")) {
		t.Errorf("got %q, expected the é to be dropped whole", got)
	}
}

func TestAttachHeaderLayout(t *testing.T) {
	data := randomBytes(100, 2)
	payload := attachHeader(data, 7, 5000, 9, 0xDEADBEEF, "notes.txt")

	expectedLength := 13 + maxNameSize + len(data) + 4
	if len(payload) != expectedLength {
		t.Fatalf("payload is %d bytes, expected %d", len(payload), expectedLength)
	}
	if payload[0] != versionByte {
		t.Errorf("version byte is %d", payload[0])
	}
	if binary.BigEndian.Uint32(payload[1:5]) != 0xDEADBEEF {
		t.Errorf("id is wrong")
	}
	if binary.BigEndian.Uint32(payload[5:9]) != 5000 {
		t.Errorf("total length is wrong")
	}
	if binary.BigEndian.Uint16(payload[9:11]) != 9 {
		t.Errorf("total chunks is wrong")
	}
	if binary.BigEndian.Uint16(payload[11:13]) != 7 {
		t.Errorf("chunk index is wrong")
	}
	if trimName(string(payload[13:13+maxNameSize])) != "notes.txt" {
		t.Errorf("name is %q", payload[13:13+maxNameSize])
	}
	if !bytes.Equal(payload[13+maxNameSize:len(payload)-4], data) {
		t.Errorf("data section doesn't match")
	}
	trailer := binary.BigEndian.Uint32(payload[len(payload)-4:])
	if trailer != checksum(payload[:len(payload)-4]) {
		t.Errorf("trailing checksum doesn't match the CRC of the rest")
	}
}

func TestMainEncodeWritesFrames(t *testing.T) {
	outputDirectory := t.TempDir()
	data := randomBytes(payloadSize*3+10, 3)

	err := mainEncode(data, "frames.bin", outputDirectory)
	if err != nil {
		t.Fatalf("mainEncode failed: %v", err)
	}

	frames := encodedFramePaths(t, outputDirectory)
	if len(frames) != 4 {
		t.Errorf("got %d frames, expected 4", len(frames))
	}
}

// encodedFramePaths finds the PNGs mainEncode wrote, it uses a random id folder so we look it up
func encodedFramePaths(t testing.TB, outputDirectory string) []string {
	t.Helper()
	entries, err := os.ReadDir(outputDirectory)
	if err != nil {
		t.Fatalf("reading output directory: %v", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("expected exactly one id folder, found %d entries", len(entries))
	}

	paths, err := filepath.Glob(filepath.Join(outputDirectory, entries[0].Name(), "*.png"))
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	return paths
}

func BenchmarkChunker(b *testing.B) {
	data := randomBytes(1<<20, 4)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		chunker(data)
	}
}

func BenchmarkAttachHeader(b *testing.B) {
	data := randomBytes(payloadSize, 5)
	for b.Loop() {
		attachHeader(data, 3, 1<<20, 1311, 42, "benchmark.bin")
	}
}

func BenchmarkQREncodeToFrame(b *testing.B) {
	payload := attachHeader(randomBytes(payloadSize, 6), 0, payloadSize, 1, 42, "benchmark.bin")
	for b.Loop() {
		payloadImage(b, payload)
	}
}

// Full mainEncode cost per chunk, including PNG compression and the disk write
func BenchmarkMainEncodePerChunk(b *testing.B) {
	chunkCount := 10
	data := randomBytes(payloadSize*chunkCount, 7)
	outputDirectory := b.TempDir()
	for b.Loop() {
		err := mainEncode(data, "benchmark.bin", outputDirectory)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*chunkCount)/1e6, "ms/chunk")
}
