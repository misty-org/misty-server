package api

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func TestingVisualSegmentBounds(timestamp, chunkEnd int64) (int64, int64) {
	return timestamp, minInt64(chunkEnd, timestamp+5_000)
}

func TestingValidJPEGPreview(raw []byte) bool {
	return len(raw) >= 4 && raw[0] == 0xff && raw[1] == 0xd8 && raw[len(raw)-2] == 0xff && raw[len(raw)-1] == 0xd9
}

func TestingValidMP3Preview(raw []byte) bool {
	return len(raw) >= 3 && (string(raw[:3]) == "ID3" || (raw[0] == 0xff && raw[1]&0xe0 == 0xe0))
}
