package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type LibraryPeopleProcessor interface {
	Analyze(context.Context, io.Reader, string, int64, bool, bool) ([]db.LibraryPeopleDetection, error)
}

type HTTPLibraryPeopleProcessor struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func NewHTTPLibraryPeopleProcessor(endpoint, token string) (*HTTPLibraryPeopleProcessor, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")) {
		return nil, errors.New("Library People processor must use HTTPS or loopback HTTP")
	}
	return &HTTPLibraryPeopleProcessor{Endpoint: parsed.String(), Token: strings.TrimSpace(token), Client: &http.Client{Timeout: 45 * time.Second}}, nil
}

func (p *HTTPLibraryPeopleProcessor) Analyze(ctx context.Context, body io.Reader, mimeType string, byteSize int64, people, pets bool) ([]db.LibraryPeopleDetection, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, io.LimitReader(body, byteSize+1))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", mimeType)
	request.Header.Set("Content-Length", strconv.FormatInt(byteSize, 10))
	request.Header.Set("X-Misty-Detect-People", strconv.FormatBool(people))
	request.Header.Set("X-Misty-Detect-Pets", strconv.FormatBool(pets))
	if p.Token != "" {
		request.Header.Set("Authorization", "Bearer "+p.Token)
	}
	response, err := p.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("People processor returned status %d", response.StatusCode)
	}
	var result struct {
		Detections []db.LibraryPeopleDetection `json:"detections"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil || len(result.Detections) > 100 {
		return nil, errors.New("People processor returned an invalid response")
	}
	return result.Detections, nil
}

func (s *SpaceLibraryService) SetPeopleProcessor(processor LibraryPeopleProcessor) {
	s.peopleProcessor = processor
}

func (s *SpaceLibraryService) ProcessPeopleJobs(ctx context.Context, workerID string, limit int) (int, error) {
	if s.peopleProcessor == nil || !s.peopleEnabled || limit < 1 {
		return 0, nil
	}
	if limit > 20 {
		limit = 20
	}
	processed := 0
	for processed < limit {
		job, err := s.database.ClaimLibraryPeopleJob(ctx, workerID, 2*time.Minute)
		if err != nil {
			return processed, err
		}
		if job == nil {
			return processed, nil
		}
		if job.ByteSize > 30<<20 {
			_ = s.database.FailLibraryPeopleJob(ctx, job, "people_image_too_large")
			processed++
			continue
		}
		reader, metadata, err := s.TestingStore.Open(ctx, job.ObjectKey)
		if err != nil || metadata.ByteSize != job.ByteSize {
			_ = s.database.FailLibraryPeopleJob(ctx, job, "people_source_unavailable")
			processed++
			continue
		}
		var policy struct {
			People bool `json:"people"`
			Pets   bool `json:"pets"`
		}
		_ = json.Unmarshal(job.Payload, &policy)
		detections, analyzeErr := s.peopleProcessor.Analyze(ctx, reader, job.MIMEType, job.ByteSize, policy.People, policy.Pets)
		_ = reader.Close()
		if analyzeErr != nil {
			_ = s.database.FailLibraryPeopleJob(ctx, job, "people_processor_failed")
			processed++
			continue
		}
		if err := s.database.CompleteLibraryPeopleJob(ctx, job, detections); err != nil {
			_ = s.database.FailLibraryPeopleJob(ctx, job, "people_result_invalid")
			processed++
			continue
		}
		processed++
	}
	return processed, nil
}
