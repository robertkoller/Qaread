package encoding

import (
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"os"

	go_qr "github.com/piglig/go-qr"
)

type QR struct {
	id          uint32
	length      uint32
	totalChunks uint16
	chunkIndex  uint16
	name        string
	data        []byte
}

func decodeQRPath(path string) (QR, error) {
	rawData, err := decodeQRBytesPath(path)
	if err != nil {
		return QR{}, err
	}

	return unwrapBytes(rawData)

	/*homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	seperator := string(os.PathSeparator)
	path := homeDir + "Qaread" + seperator + "output" + seperator + string(id) + seperator + "frame" + "(" + string(i) + ")" + ".png"*/
}

func decodeQRImg(image image.Image) (QR, error) {
	rawData, err := decodeQRBytesImg(image)
	if err != nil {
		return QR{}, err
	}

	return unwrapBytes(rawData)
}

func decodeQRBytesImg(image image.Image) ([]byte, error) {
	text, err := go_qr.Decode(image)
	if err != nil {
		return nil, err
	}

	return []byte(text), nil
}

func decodeQRBytesPath(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	img, _ := png.Decode(file)
	text, err := go_qr.Decode(img)
	if err != nil {
		return nil, err
	}

	return []byte(text), nil
}

// 0 - version
// 1-4 fileID
// 5-8 totalLength of file
// 9-10 totalChunks
// 11-12 chunkIndex
// maxNameSize bytes for the name of the file
// data
// 4 bytes checksum

func unwrapBytes(data []byte) (QR, error) {
	if len(data) < 17 {
		return QR{}, errors.New("Bytes aren't long enough, payload probably broken")
	}

	if checksum(data[:(len(data)-4)]) != binary.BigEndian.Uint32(data[:(len(data)-4)]) {
		return QR{}, errors.New("Invalid byte buffer checksum")
	}

	id := binary.BigEndian.Uint32(data[1:])
	length := binary.BigEndian.Uint32(data[5:])
	totalChunks := binary.BigEndian.Uint16(data[9:])
	chunkIndex := binary.BigEndian.Uint16(data[11:])
	name := string(data[13:maxNameSize])
	output := data[13+maxNameSize : len(data)-4]

	return QR{
		id:          id,
		length:      length,
		totalChunks: totalChunks,
		chunkIndex:  chunkIndex,
		name:        name,
		data:        output,
	}, nil

}
