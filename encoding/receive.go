package encoding

import (
	"bytes"
	"errors"
	"image"
)

type File struct {
	id          uint32
	frames      [][]byte
	totalChunks uint16
	count       uint16
	name        string
	started     bool
}

// receiveFrames waits for images to come in on frames and decodes each one until every chunk of the file has arrived
func (file *File) receiveFrames(frames <-chan image.Image) error {
	for frame := range frames {
		currQR, err := decodeQRImg(frame)
		if err != nil {
			continue
		}

		if !file.started {
			file.id = currQR.id
			file.totalChunks = currQR.totalChunks
			file.frames = make([][]byte, currQR.totalChunks)
			file.started = true
			file.name = currQR.name

			if currQR.chunkIndex >= currQR.totalChunks {
				return errors.New("chunk index out of bounds of total chunks, some error somewhere")
			}
			file.frames[currQR.chunkIndex] = currQR.data
			file.count = 1

			if file.totalChunks == 1 {
				return nil
			}
			continue
		}

		if currQR.id != file.id {
			return errors.New("ID of QR does not match downloading file ID")
		}

		if currQR.chunkIndex >= file.totalChunks {
			return errors.New("chunk index out of bounds of total chunks, some error somewhere")
		}

		if file.frames[currQR.chunkIndex] != nil {
			continue
		}

		file.frames[currQR.chunkIndex] = currQR.data
		file.count++

		if file.count == file.totalChunks {
			return nil
		}

	}
	return errors.New("Channel closed before file was complete")
}

// this assembles a fully completed file
func assembleCompletedFile(file *File) ([]byte, error) {
	if file.count != uint16(len(file.frames)) {
		return nil, errors.New("Chunk count is incorrect")
	}

	if file.count != file.totalChunks {
		return nil, errors.New("Total chunk count does not equal the number of current chunks")
	}

	return bytes.Join(file.frames, nil), nil
}
