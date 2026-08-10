package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type LibraryMediaProcessor interface {
	Render(context.Context, io.Reader, string, int64, db.LibraryEditDefinition, int64) (*RenderedLibraryMedia, error)
	Preview(context.Context, io.Reader, int64, int) (*RenderedLibraryMedia, error)
}

type RenderedLibraryMedia struct {
	path     string
	tempDir  string
	MIMEType string
	ByteSize int64
	SHA256   string
}

func (media *RenderedLibraryMedia) Open() (io.ReadCloser, error) {
	if media == nil || media.path == "" {
		return nil, errors.New("rendered Library media is unavailable")
	}
	return os.Open(media.path)
}

func (media *RenderedLibraryMedia) Cleanup() {
	if media != nil && media.tempDir != "" {
		_ = os.RemoveAll(media.tempDir)
	}
}

// FFmpegLibraryMediaProcessor invokes a fixed executable directly (never a
// shell) with server-generated paths and validated numeric edit values. Input
// protocols, output formats, execution time, threads, and output size are all
// bounded so untrusted filenames and embedded network URLs cannot become
// command arguments or unbounded work.
type FFmpegLibraryMediaProcessor struct {
	Executable    string
	PDFExecutable string
	Timeout       time.Duration
}

func NewFFmpegLibraryMediaProcessor(executable string) (*FFmpegLibraryMediaProcessor, error) {
	path, err := exec.LookPath(strings.TrimSpace(executable))
	if err != nil {
		return nil, fmt.Errorf("find Library media processor: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	pdfPath := ""
	if candidate, lookupErr := exec.LookPath("pdftoppm"); lookupErr == nil {
		pdfPath, _ = filepath.Abs(candidate)
	}
	return &FFmpegLibraryMediaProcessor{Executable: path, PDFExecutable: pdfPath, Timeout: 12 * time.Minute}, nil
}

func (processor *FFmpegLibraryMediaProcessor) Render(ctx context.Context, source io.Reader, mimeType string, sourceBytes int64, definition db.LibraryEditDefinition, maximumBytes int64) (*RenderedLibraryMedia, error) {
	if processor == nil || processor.Executable == "" || source == nil || sourceBytes < 1 || maximumBytes < 1 || maximumBytes > db.MaxSpaceStorageBytes {
		return nil, db.ErrLibraryInvalid
	}
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	isImage, isVideo := strings.HasPrefix(mimeType, "image/"), strings.HasPrefix(mimeType, "video/")
	if !isImage && !isVideo || definition.Validate(mimeType) != nil {
		return nil, db.ErrLibraryInvalid
	}
	tempDir, err := os.MkdirTemp("", "misty-library-render-*")
	if err != nil {
		return nil, err
	}
	cleanup := func(err error) (*RenderedLibraryMedia, error) {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	inputPath := filepath.Join(tempDir, "input"+libraryMediaInputExtension(mimeType))
	input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return cleanup(err)
	}
	written, copyErr := io.Copy(input, io.LimitReader(source, sourceBytes+1))
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil || written != sourceBytes {
		return cleanup(errors.New("Library rendition source size mismatch"))
	}
	outputMIME, outputName := "image/jpeg", "output.jpg"
	if isVideo {
		outputMIME, outputName = "video/mp4", "output.mp4"
	}
	outputPath := filepath.Join(tempDir, outputName)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe,crypto,data", "-threads", "2", "-filter_threads", "2"}
	if isVideo && definition.Trim != nil {
		args = append(args, "-ss", formatMediaNumber(definition.Trim.Start))
	}
	args = append(args, "-i", inputPath)
	if isVideo && definition.Trim != nil {
		args = append(args, "-t", formatMediaNumber(definition.Trim.End-definition.Trim.Start))
	}
	if filters := libraryVideoFilters(definition); len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	if isImage {
		args = append(args, "-map", "0:v:0", "-frames:v", "1", "-an", "-map_metadata", "-1", "-q:v", "2", "-f", "image2", "-y", outputPath)
	} else {
		args = append(args, "-map", "0:v:0")
		if definition.Mute {
			args = append(args, "-an")
		} else {
			args = append(args, "-map", "0:a?")
			if definition.PlaybackSpeed > 0 && definition.PlaybackSpeed != 1 {
				args = append(args, "-filter:a", "atempo="+formatMediaNumber(definition.PlaybackSpeed))
			}
			args = append(args, "-c:a", "aac", "-b:a", "192k")
		}
		args = append(args, "-map_metadata", "-1", "-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-fs", strconv.FormatInt(maximumBytes, 10), "-f", "mp4", "-y", outputPath)
	}
	timeout := processor.Timeout
	if timeout <= 0 || timeout > 30*time.Minute {
		timeout = 12 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, processor.Executable, args...)
	command.Dir = tempDir
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + tempDir, "TMPDIR=" + tempDir, "LC_ALL=C"}
	var stderr cappedBuffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return cleanup(errors.New("Library media processor timed out"))
		}
		return cleanup(fmt.Errorf("Library media processor failed: %w: %s", err, stderr.String()))
	}
	if isImage {
		if err := applyLibraryCleanup(outputPath, definition.Markup); err != nil {
			return cleanup(err)
		}
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() < 1 || info.Size() > maximumBytes {
		return cleanup(errors.New("Library media processor produced an invalid output size"))
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return cleanup(err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, info.Size()+1)); err != nil {
		file.Close()
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		return cleanup(err)
	}
	return &RenderedLibraryMedia{path: outputPath, tempDir: tempDir, MIMEType: outputMIME, ByteSize: info.Size(), SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}

func (processor *FFmpegLibraryMediaProcessor) Preview(ctx context.Context, source io.Reader, sourceBytes int64, maximumDimension int) (*RenderedLibraryMedia, error) {
	if processor == nil || processor.Executable == "" || source == nil || sourceBytes < 1 || maximumDimension < 256 || maximumDimension > 4096 {
		return nil, db.ErrLibraryInvalid
	}
	tempDir, err := os.MkdirTemp("", "misty-library-preview-*")
	if err != nil {
		return nil, err
	}
	cleanup := func(err error) (*RenderedLibraryMedia, error) {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	inputPath, outputPath := filepath.Join(tempDir, "input.bin"), filepath.Join(tempDir, "preview.jpg")
	input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return cleanup(err)
	}
	written, copyErr := io.Copy(input, io.LimitReader(source, sourceBytes+1))
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil || written != sourceBytes {
		return cleanup(errors.New("Library preview source size mismatch"))
	}
	dimension := strconv.Itoa(maximumDimension)
	magic := make([]byte, 5)
	magicFile, magicErr := os.Open(inputPath)
	if magicErr != nil {
		return cleanup(magicErr)
	}
	_, magicErr = io.ReadFull(magicFile, magic)
	_ = magicFile.Close()
	isPDF := magicErr == nil && bytes.Equal(magic, []byte("%PDF-"))
	executable := processor.Executable
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe,crypto,data", "-threads", "2", "-filter_threads", "2", "-i", inputPath, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale='min(" + dimension + ",iw)':'min(" + dimension + ",ih)':force_original_aspect_ratio=decrease", "-an", "-map_metadata", "-1", "-q:v", "3", "-f", "image2", "-y", outputPath}
	if isPDF {
		if processor.PDFExecutable == "" {
			return cleanup(errors.New("PDF preview generation requires pdftoppm"))
		}
		executable = processor.PDFExecutable
		args = []string{"-f", "1", "-l", "1", "-singlefile", "-jpeg", "-scale-to", dimension, inputPath, strings.TrimSuffix(outputPath, filepath.Ext(outputPath))}
	}
	timeout := processor.Timeout
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 2 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, executable, args...)
	command.Dir = tempDir
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + tempDir, "TMPDIR=" + tempDir, "LC_ALL=C"}
	var stderr cappedBuffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return cleanup(fmt.Errorf("Library preview generation failed: %w: %s", err, stderr.String()))
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() < 1 || info.Size() > 25_000_000 {
		return cleanup(errors.New("Library preview output is invalid"))
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return cleanup(err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, info.Size()+1)); err != nil {
		file.Close()
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		return cleanup(err)
	}
	return &RenderedLibraryMedia{path: outputPath, tempDir: tempDir, MIMEType: "image/jpeg", ByteSize: info.Size(), SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}
