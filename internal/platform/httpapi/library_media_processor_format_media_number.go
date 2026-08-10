package api

import (
	"bytes"
	"strconv"
)

func formatMediaNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

type cappedBuffer struct{ bytes.Buffer }

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if buffer.Len() < 8192 {
		remaining := 8192 - buffer.Len()
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.Buffer.Write(value)
	}
	return original, nil
}
