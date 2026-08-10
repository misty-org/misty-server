package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type LibraryMetadataExtractor interface {
	Extract(context.Context, LibraryObjectStore, string, int64) (map[string]any, error)
}

type FFprobeLibraryMetadataExtractor struct {
	Executable string
	Timeout    time.Duration
}

func NewFFprobeLibraryMetadataExtractor(mediaExecutable string) (*FFprobeLibraryMetadataExtractor, error) {
	mediaPath, err := exec.LookPath(strings.TrimSpace(mediaExecutable))
	if err != nil {
		return nil, err
	}
	probePath := filepath.Join(filepath.Dir(mediaPath), "ffprobe")
	if _, err := os.Stat(probePath); err != nil {
		probePath, err = exec.LookPath("ffprobe")
		if err != nil {
			return nil, err
		}
	}
	probePath, err = filepath.Abs(probePath)
	if err != nil {
		return nil, err
	}
	return &FFprobeLibraryMetadataExtractor{Executable: probePath, Timeout: 2 * time.Minute}, nil
}

type ffprobeMetadata struct {
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		AverageRate string `json:"avg_frame_rate"`
		Channels    int    `json:"channels"`
		SampleRate  string `json:"sample_rate"`
	} `json:"streams"`
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

func (extractor *FFprobeLibraryMetadataExtractor) Extract(ctx context.Context, store LibraryObjectStore, objectKey string, byteSize int64) (map[string]any, error) {
	if extractor == nil || extractor.Executable == "" || store == nil || objectKey == "" || byteSize < 1 {
		return nil, errors.New("invalid Library metadata extraction request")
	}
	tempDir, err := os.MkdirTemp("", "misty-library-metadata-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	reader, metadata, err := store.Open(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if metadata.ByteSize != byteSize {
		return nil, errors.New("Library metadata object size mismatch")
	}
	path := filepath.Join(tempDir, "asset")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, byteSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != byteSize {
		return nil, errors.New("Library metadata source size mismatch")
	}
	timeout := extractor.Timeout
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 2 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, extractor.Executable, "-v", "error", "-probesize", "10000000", "-analyzeduration", "10000000", "-show_entries", "stream=codec_type,codec_name,width,height,avg_frame_rate,channels,sample_rate:format=duration:format_tags=creation_time,com.apple.quicktime.creationdate,com.apple.quicktime.location.ISO6709,location", "-of", "json", path)
	command.Dir = tempDir
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + tempDir, "TMPDIR=" + tempDir, "LC_ALL=C"}
	var output cappedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return nil, err
	}
	var probe ffprobeMetadata
	if err := json.Unmarshal(output.Bytes(), &probe); err != nil {
		return nil, err
	}
	result := map[string]any{}
	codecs := []string{}
	for _, stream := range probe.Streams {
		if stream.CodecName != "" {
			codecs = append(codecs, stream.CodecType+":"+stream.CodecName)
		}
		if stream.Width > 0 && stream.Height > 0 {
			result["width"], result["height"] = stream.Width, stream.Height
		}
		if rate := parseFFprobeRate(stream.AverageRate); rate > 0 {
			result["frame_rate"] = rate
		}
		if stream.Channels > 0 {
			result["audio_channels"] = stream.Channels
		}
		if sampleRate, _ := strconv.Atoi(stream.SampleRate); sampleRate > 0 {
			result["audio_sample_rate"] = sampleRate
		}
	}
	if len(codecs) > 0 {
		result["codecs"] = codecs
	}
	if duration, _ := strconv.ParseFloat(probe.Format.Duration, 64); duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
		result["duration"] = duration
	}
	for _, key := range []string{"creation_time", "com.apple.quicktime.creationdate"} {
		if value := strings.TrimSpace(probe.Format.Tags[key]); value != "" {
			if captured, err := time.Parse(time.RFC3339, value); err == nil {
				result["capture_timestamp"] = captured.UTC().Format(time.RFC3339Nano)
				break
			}
		}
	}
	for _, key := range []string{"com.apple.quicktime.location.ISO6709", "location"} {
		if latitude, longitude, ok := TestingParseISO6709(probe.Format.Tags[key]); ok {
			result["embedded_location"] = map[string]any{"latitude": latitude, "longitude": longitude}
			break
		}
	}
	return result, nil
}

func parseFFprobeRate(value string) float64 {
	numerator, denominator, found := strings.Cut(strings.TrimSpace(value), "/")
	if !found {
		rate, _ := strconv.ParseFloat(numerator, 64)
		return rate
	}
	left, _ := strconv.ParseFloat(numerator, 64)
	right, _ := strconv.ParseFloat(denominator, 64)
	if right == 0 {
		return 0
	}
	return left / right
}

var iso6709Pattern = regexp.MustCompile(`^([+-]\d+(?:\.\d+)?)([+-]\d+(?:\.\d+)?)`)

func TestingParseISO6709(value string) (float64, float64, bool) {
	match := iso6709Pattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0, 0, false
	}
	latitude, latitudeErr := strconv.ParseFloat(match[1], 64)
	longitude, longitudeErr := strconv.ParseFloat(match[2], 64)
	return latitude, longitude, latitudeErr == nil && longitudeErr == nil && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
