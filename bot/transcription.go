package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type TranscriptionClient struct {
	baseURL    string
	httpClient *http.Client
}

type TranscriptionResponse struct {
	Text string `json:"text"`
}

func NewTranscriptionClient(baseURL string) *TranscriptionClient {
	return &TranscriptionClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *TranscriptionClient) Transcribe(audioData []byte, filename string) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", fmt.Errorf("write audio data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", c.baseURL+"/transcribe", &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("transcription failed (status %d): %s", resp.StatusCode, body)
	}

	var result TranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.Text, nil
}
