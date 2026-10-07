package encoding

import (
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"math/rand"
	"unicode/utf8"

	goQR "github.com/piglig/go-qr"
)

const payloadSize = 800
const versionByte = 0x01
const maxNameSize = 25

// pixels per QR module and the quiet zone width in modules around the code
const frameScale = 10
const frameBorder = 4

// BestSpeed is fine here since a black and white image compresses well at any level
var framePNGEncoder = png.Encoder{CompressionLevel: png.BestSpeed}

// takes data bytes and encodes it, writing the frames to outputDirectory
func mainEncode(data []byte, name string, outputDirectory string) error {
	chunked := chunker(data)

	id := rand.Uint32()
	length := uint32(len(data))
	chunks := uint16(len(chunked))

	// os.Create won't make missing folders so we make the frame folder up front
	frameDirectory := filepath.Join(outputDirectory, strconv.FormatUint(uint64(id), 10))
	err := os.MkdirAll(frameDirectory, 0755)
	if err != nil {
		return err
	}

	for i, chunk := range chunked {
		headed := attachHeader(chunk, i, length, chunks, id, name)
		//encoded := base64.StdEncoding.EncodeToString(headed)
		errLevel := goQR.High
		code, err := goQR.EncodeBinary(headed, errLevel)
		if err != nil {
			return err
		}

		path := filepath.Join(frameDirectory, "frame("+strconv.Itoa(i)+").png")

		err = writeFramePNG(renderFrame(code, frameScale, frameBorder), path)
		if err != nil {
			return err
		}
	}

	return nil

}

// renderFrame draws the QR code into a grayscale image by writing pixels directly.
// go-qr's ToImage sets one pixel at a time and allocates for each one which made it about 50x slower.
// We use Gray because go-qr's decoder has a fast path for it, Paletted made decoding nearly 3x slower
func renderFrame(code *goQR.QrCode, scale int, border int) *image.Gray {
	modulesPerSide := code.Size() + border*2
	pixelsPerSide := modulesPerSide * scale
	frame := image.NewGray(image.Rect(0, 0, pixelsPerSide, pixelsPerSide))

	// start fully white so we only need to paint the dark modules
	for index := range frame.Pix {
		frame.Pix[index] = 255
	}
	for moduleY := 0; moduleY < modulesPerSide; moduleY++ {
		for moduleX := 0; moduleX < modulesPerSide; moduleX++ {
			if !code.Module(moduleX-border, moduleY-border) {
				continue
			}
			for pixelY := moduleY * scale; pixelY < (moduleY+1)*scale; pixelY++ {
				row := frame.Pix[pixelY*frame.Stride:]
				for pixelX := moduleX * scale; pixelX < (moduleX+1)*scale; pixelX++ {
					row[pixelX] = 0
				}
			}
		}
	}

	return frame
}

func writeFramePNG(frame image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}

	err = framePNGEncoder.Encode(file, frame)
	if err != nil {
		file.Close()
		return err
	}

	// Close can report a failed write so we don't just defer it
	return file.Close()
}

// 0 - version
// 1-4 fileID
// 5-8 totalLength of file
// 9-10 totalChunks
// 11-12 chunkIndex
// maxNameSize bytes for the name of the file
// data
// 4 bytes checksum
func attachHeader(data []byte, index int, totalLength uint32, totalChunks uint16, id uint32, name string) []byte {
	buffer := make([]byte, 13+maxNameSize)
	buffer[0] = versionByte
	binary.BigEndian.PutUint32(buffer[1:], id)
	binary.BigEndian.PutUint32(buffer[5:], totalLength)
	binary.BigEndian.PutUint16(buffer[9:], totalChunks)
	binary.BigEndian.PutUint16(buffer[11:], uint16(index))
	copy(buffer[13:], createNameBuffer(name, maxNameSize))

	buffer = append(buffer, data...)

	sum := checksum(buffer)
	sumBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(sumBuf, sum)

	return append(buffer, sumBuf...)

}

func checksum(data []byte) uint32 {
	table := crc32.MakeTable(crc32.Castagnoli)
	sum := crc32.Checksum(data, table)
	return sum
}

// makes the buffer for the name of the file
func createNameBuffer(name string, maxBytes int) []byte {
	if len(name) <= maxBytes {
		return []byte(name)
	}

	b := []byte(name[:maxBytes])
	// cutting at maxBytes can split a multi byte character so we back off until it's valid again
	for !utf8.Valid(b) && len(b) > 0 {
		b = b[:len(b)-1]
	}
	return b
}

// we split the input into a manageable size for encoding
func chunker(data []byte) [][]byte {
	var output [][]byte
	for len(data) > 0 {
		length := min(payloadSize, len(data))
		output = append(output, data[:length:length])
		data = data[length:]
	}

	return output
}
