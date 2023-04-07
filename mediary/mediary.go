package mediary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/hori-ryota/zaperr"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

//go:generate moq -out mediarymocks/service.go -pkg mediarymocks -rm . Service:ServiceMock
type Service interface {
	IsValidURL(ctx context.Context, mediaURL string) (bool, error)
	FetchMetadataLongPolling(ctx context.Context, mediaURL string) (*Metadata, error)
	CreateUploadJob(ctx context.Context, params *CreateUploadJobParams) (jobID string, err error)
	FetchJobStatusMap(ctx context.Context, jobIDs []string) (map[string]*JobStatus, error)
}

func New(mediaryURL string, logger *zap.Logger) Service {
	return &service{
		logger:  logger,
		baseURL: mediaryURL,
	}
}

type service struct {
	logger  *zap.Logger
	baseURL string
}

type Metadata struct {
	URL                   string    `json:"url"`
	Name                  string    `json:"name"`
	Variants              []Variant `json:"variants"`
	AllowMultipleVariants bool      `json:"allow_multiple_variants"`
}

type Variant struct {
	ID       string `json:"id"`
	LenBytes *int64 `json:"length_bytes"`
}

type CreateUploadJobParams struct {
	URL    string               `json:"url"`
	Type   JobType              `json:"type"`
	Params ConcatenateJobParams `json:"params"`
}

type JobType string

var JobTypeConcatenate JobType = "concatenate"

type ConcatenateJobParams struct {
	Variants   []string `json:"variants"`
	AudioCodec string   `json:"audioCodec"`
	UploadURL  string   `json:"uploadUrl"`
}

type JobStatus struct {
	Id                  string        `json:"id"`
	Status              JobStatusName `json:"status"`
	ResultMediaDuration time.Duration `json:"result_media_duration"`
	ResultFileBytes     int64         `json:"result_file_bytes"`
}

type JobStatusName string

const (
	JobStatusAccepted    JobStatusName = "accepted"
	JobStatusCreated     JobStatusName = "created"
	JobStatusDownloading JobStatusName = "downloading"
	JobStatusProcessing  JobStatusName = "processing"
	JobStatusUploading   JobStatusName = "uploading"
	JobStatusComplete    JobStatusName = "complete"
)

func (svc *service) IsValidURL(ctx context.Context, mediaURL string) (bool, error) {
	// TODO: should not depend on metadata endpoint, implement /is_valid in mediary
	fullURL := fmt.Sprintf("%s/metadata/long-polling?url=%s", svc.baseURL, mediaURL)
	svc.logger.Debug("checking if URL is valid", zap.String("url", fullURL))

	resp, err := http.Get(fullURL)
	if err != nil {
		return false, fmt.Errorf("failed to call mediary API: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("mediary returned status code %d", resp.StatusCode)
	}

	return true, nil
}

func (svc *service) FetchMetadataLongPolling(ctx context.Context, mediaURL string) (*Metadata, error) {
	fullURL := fmt.Sprintf("%s/metadata/long-polling?url=%s", svc.baseURL, mediaURL)
	svc.logger.Debug("fetching metadata", zap.String("url", fullURL))

	resp, err := http.Get(fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to call mediary API: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mediary returned status code %d", resp.StatusCode)
	}

	var metadata Metadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("error decoding mediary response: %w", err)
	}

	return &metadata, nil
}

func (svc *service) CreateUploadJob(ctx context.Context, params *CreateUploadJobParams) (jobID string, err error) {
	fullURL := fmt.Sprintf("%s/jobs", svc.baseURL)
	svc.logger.Debug("creating upload job", zap.String("url", fullURL))

	payload, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := http.Post(fullURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to call mediary API: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("mediary returned status code %d", resp.StatusCode)
	}

	type response struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	var respBody response
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return "", fmt.Errorf("error decoding mediary response: %w", err)
	}
	if respBody.Status != "accepted" {
		return "", fmt.Errorf("mediary returned status %s", respBody.Status)
	}

	return respBody.ID, nil
}

func (svc *service) FetchJobStatusMap(ctx context.Context, jobIDs []string) (map[string]*JobStatus, error) {
	// TODO: implement bulk job status fetching on mediary side
	var wg sync.WaitGroup
	jobStatusChan := make(chan *JobStatus, len(jobIDs))
	for _, jobID := range jobIDs {
		wg.Add(1)

		go func(jobID string) {
			defer wg.Done()

			fullURL := fmt.Sprintf("%s/jobs/%s", svc.baseURL, jobID)
			svc.logger.Debug("fetching job status", zap.String("url", fullURL))
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
			if err != nil {
				svc.logger.Error("failed to create request", zaperr.ToField(err))
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				svc.logger.Error("failed to call mediary API", zaperr.ToField(err))
				return
			}
			var jobStatus JobStatus
			if err := json.NewDecoder(resp.Body).Decode(&jobStatus); err != nil {
				svc.logger.Error("error decoding mediary response", zaperr.ToField(err))
				return
			}
			jobStatusChan <- &jobStatus
		}(jobID)
	}

	wg.Wait()
	close(jobStatusChan)

	jobStatusMap := make(map[string]*JobStatus, len(jobIDs))
	for jobStatus := range jobStatusChan {
		jobStatusMap[jobStatus.Id] = jobStatus
	}

	return jobStatusMap, nil
}
