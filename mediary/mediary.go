package mediary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

//go:generate moq -out mediarymocks/service.go -pkg mediarymocks -rm . Service:ServiceMock
type Service interface {
	IsValidURL(ctx context.Context, mediaURL string) (bool, error)
	FetchMetadataLongPolling(ctx context.Context, mediaURL string) (*Metadata, error)
	CreateUploadJob(ctx context.Context, params *CreateUploadJobParams) (jobID string, err error)
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
	URL   string         `json:"url"`
	Name  string         `json:"name"`
	Files []FileMetadata `json:"files"`
}

type FileMetadata struct {
	Path     string `json:"path"`
	LenBytes int64  `json:"length_bytes"`
}

type CreateUploadJobParams struct {
	URL    string               `json:"url"`
	Type   JobType              `json:"type"`
	Params ConcatenateJobParams `json:"params"`
}

type JobType string

var JobTypeConcatenate JobType = "concatenate"

type ConcatenateJobParams struct {
	Filepaths  []string `json:"filepaths"`
	AudioCodec string   `json:"audioCodec"`
	UploadURL  string   `json:"uploadUrl"`
}

func (svc *service) IsValidURL(ctx context.Context, mediaURL string) (bool, error) {
	fullURL := fmt.Sprintf("%s/metadata?url=%s", svc.baseURL, mediaURL)
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
