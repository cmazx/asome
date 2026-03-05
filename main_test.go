package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type stubRepository struct {
	createDocumentFn           func(ctx context.Context, doc *document) error
	temporalSearchFn           func(ctx context.Context, embedding []float64, params temporalSearchParams) ([]searchResult, error)
	listPendingDocumentsFn     func(ctx context.Context, limit int) ([]document, error)
	clearDocumentChunksFn      func(ctx context.Context, documentID uuid.UUID) error
	appendDocumentChunksFn     func(ctx context.Context, chunks []chunk) error
	markDocumentProcessedFn    func(ctx context.Context, documentID uuid.UUID, processingTimeSeconds int) error
	setDocumentProcessingErrFn func(ctx context.Context, documentID uuid.UUID, processingError string) error
}

func (s *stubRepository) createDocument(ctx context.Context, doc *document) error {
	if s.createDocumentFn != nil {
		return s.createDocumentFn(ctx, doc)
	}
	return nil
}

func (s *stubRepository) temporalSearch(ctx context.Context, embedding []float64, params temporalSearchParams) ([]searchResult, error) {
	if s.temporalSearchFn != nil {
		return s.temporalSearchFn(ctx, embedding, params)
	}
	return nil, nil
}

func (s *stubRepository) listPendingDocuments(ctx context.Context, limit int) ([]document, error) {
	if s.listPendingDocumentsFn != nil {
		return s.listPendingDocumentsFn(ctx, limit)
	}
	return nil, nil
}

func (s *stubRepository) clearDocumentChunks(ctx context.Context, documentID uuid.UUID) error {
	if s.clearDocumentChunksFn != nil {
		return s.clearDocumentChunksFn(ctx, documentID)
	}
	return nil
}

func (s *stubRepository) appendDocumentChunks(ctx context.Context, chunks []chunk) error {
	if s.appendDocumentChunksFn != nil {
		return s.appendDocumentChunksFn(ctx, chunks)
	}
	return nil
}

func (s *stubRepository) markDocumentProcessed(ctx context.Context, documentID uuid.UUID, processingTimeSeconds int) error {
	if s.markDocumentProcessedFn != nil {
		return s.markDocumentProcessedFn(ctx, documentID, processingTimeSeconds)
	}
	return nil
}

func (s *stubRepository) setDocumentProcessingError(ctx context.Context, documentID uuid.UUID, processingError string) error {
	if s.setDocumentProcessingErrFn != nil {
		return s.setDocumentProcessingErrFn(ctx, documentID, processingError)
	}
	return nil
}

type stubEmbeddingClient struct {
	embedFn func(ctx context.Context, input string) ([]float64, error)
}

func (s *stubEmbeddingClient) embed(ctx context.Context, input string) ([]float64, error) {
	if s.embedFn != nil {
		return s.embedFn(ctx, input)
	}
	return nil, nil
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(payload)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestUploadDocumentHandlerSuccess(t *testing.T) {
	tempDir := t.TempDir()
	docID := uuid.New()

	appInstance := &App{
		repo: &stubRepository{createDocumentFn: func(_ context.Context, doc *document) error {
			doc.ID = docID
			if doc.Scope != "global" {
				t.Fatalf("unexpected scope: %s", doc.Scope)
			}
			if doc.Version != 2 {
				t.Fatalf("unexpected version: %d", doc.Version)
			}
			return nil
		}},
		tempDir: tempDir,
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := writer.CreateFormFile("file", "doc.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err = io.Copy(fileWriter, strings.NewReader("hello")); err != nil {
		t.Fatalf("write file content: %v", err)
	}
	_ = writer.WriteField("scope", "global")
	_ = writer.WriteField("source", "manual")
	_ = writer.WriteField("doc_type", "policy")
	_ = writer.WriteField("version", "2")
	if err = writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()

	appInstance.uploadDocumentHandler(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, res.Code)
	}

	var response uploadResponse
	if err = json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != docID {
		t.Fatalf("unexpected response id: %s", response.ID)
	}
	if !strings.HasPrefix(response.TemporalPath, tempDir) {
		t.Fatalf("unexpected temporal path: %s", response.TemporalPath)
	}
	if _, err = os.Stat(response.TemporalPath); err != nil {
		t.Fatalf("temporary file was not saved: %v", err)
	}
}

func TestUploadDocumentHandlerValidation(t *testing.T) {
	tempDir := t.TempDir()
	appInstance := &App{repo: &stubRepository{}, tempDir: tempDir}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := writer.CreateFormFile("file", "doc.md")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err = io.Copy(fileWriter, strings.NewReader("hello")); err != nil {
		t.Fatalf("write file content: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()

	appInstance.uploadDocumentHandler(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
}

func TestSearchHandlerDefaultParameters(t *testing.T) {
	fixedTime := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	var capturedParams temporalSearchParams
	var capturedEmbedding []float64

	appInstance := &App{
		now: func() time.Time { return fixedTime },
		embeddingClient: &stubEmbeddingClient{embedFn: func(_ context.Context, input string) ([]float64, error) {
			if input != "test query" {
				t.Fatalf("unexpected query: %s", input)
			}
			return []float64{0.1, 0.2}, nil
		}},
		repo: &stubRepository{temporalSearchFn: func(_ context.Context, embedding []float64, params temporalSearchParams) ([]searchResult, error) {
			capturedParams = params
			capturedEmbedding = embedding
			return []searchResult{{
				ChunkID:       uuid.New(),
				DocumentID:    uuid.New(),
				Content:       "chunk",
				Title:         "doc",
				SemanticScore: 0.9,
				TemporalScore: 0.8,
				CombinedScore: 0.85,
			}}, nil
		}},
	}

	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"test query"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	appInstance.searchHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if len(capturedEmbedding) != 2 {
		t.Fatalf("unexpected embedding size: %d", len(capturedEmbedding))
	}
	if !capturedParams.QueryTime.Equal(fixedTime) {
		t.Fatalf("unexpected query_time: %v", capturedParams.QueryTime)
	}
	if capturedParams.TimeDecayDays != defaultTimeDecayDays {
		t.Fatalf("unexpected time_decay_days: %v", capturedParams.TimeDecayDays)
	}
	if capturedParams.FulltextWeight != defaultFulltextWeight {
		t.Fatalf("unexpected fulltext_weight: %v", capturedParams.FulltextWeight)
	}
	if capturedParams.ResultLimit != defaultResultLimit {
		t.Fatalf("unexpected result_limit: %d", capturedParams.ResultLimit)
	}
}

func TestSearchHandlerFulltextWeightOverride(t *testing.T) {
	var capturedParams temporalSearchParams

	appInstance := &App{
		now: time.Now,
		embeddingClient: &stubEmbeddingClient{embedFn: func(_ context.Context, _ string) ([]float64, error) {
			return []float64{0.1, 0.2}, nil
		}},
		repo: &stubRepository{temporalSearchFn: func(_ context.Context, _ []float64, params temporalSearchParams) ([]searchResult, error) {
			capturedParams = params
			return nil, nil
		}},
	}

	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"test","fulltext_weight":0.4}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	appInstance.searchHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if capturedParams.FulltextWeight != 0.4 {
		t.Fatalf("unexpected fulltext_weight: %v", capturedParams.FulltextWeight)
	}
}

func TestSearchHandlerInvalidOverride(t *testing.T) {
	appInstance := &App{
		now:             time.Now,
		embeddingClient: &stubEmbeddingClient{},
		repo:            &stubRepository{},
	}

	invalidRequests := []string{
		`{"query":"test","result_limit":0}`,
		`{"query":"test","fulltext_weight":1.2}`,
	}

	for _, body := range invalidRequests {
		req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()

		appInstance.searchHandler(res, req)

		if res.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d for body %s", http.StatusBadRequest, res.Code, body)
		}
	}
}

func TestProcessPendingDocumentsBatchSuccess(t *testing.T) {
	tempDir := t.TempDir()
	tempFilePath := filepath.Join(tempDir, "source.txt")
	content := strings.Repeat("lorem ipsum dolor sit amet ", 5000)
	if err := os.WriteFile(tempFilePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	docID := uuid.New()
	var savedChunks []chunk
	processingTimeSeconds := -1
	embedCallCount := 0
	clearCalls := 0
	appendCalls := 0
	markCalled := false

	appInstance := &App{
		embeddingClient: &stubEmbeddingClient{embedFn: func(_ context.Context, input string) ([]float64, error) {
			embedCallCount++
			if strings.TrimSpace(input) == "" {
				t.Fatal("embedding input should not be empty")
			}
			return []float64{float64(len(input)), 1}, nil
		}},
		repo: &stubRepository{
			listPendingDocumentsFn: func(_ context.Context, limit int) ([]document, error) {
				if limit != defaultProcessingBatchLimit {
					t.Fatalf("unexpected batch limit: %d", limit)
				}
				return []document{{
					ID:           docID,
					TemporalPath: tempFilePath,
					Scope:        "global",
				}}, nil
			},
			clearDocumentChunksFn: func(_ context.Context, documentID uuid.UUID) error {
				if documentID != docID {
					t.Fatalf("unexpected document id: %s", documentID)
				}
				clearCalls++
				return nil
			},
			appendDocumentChunksFn: func(_ context.Context, chunks []chunk) error {
				appendCalls++
				if len(chunks) == 0 {
					t.Fatal("unexpected empty chunk batch")
				}
				if len(chunks) > defaultChunkWriteBatchSize {
					t.Fatalf("chunk batch is too large: got %d, want <= %d", len(chunks), defaultChunkWriteBatchSize)
				}
				savedChunks = append(savedChunks, chunks...)
				return nil
			},
			markDocumentProcessedFn: func(_ context.Context, documentID uuid.UUID, savedProcessingTimeSeconds int) error {
				markCalled = true
				if documentID != docID {
					t.Fatalf("unexpected document id: %s", documentID)
				}
				if _, statErr := os.Stat(tempFilePath); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("temporary file should be removed before marking processed, stat err: %v", statErr)
				}
				processingTimeSeconds = savedProcessingTimeSeconds
				return nil
			},
			setDocumentProcessingErrFn: func(_ context.Context, documentID uuid.UUID, processingError string) error {
				t.Fatalf("unexpected processing error for %s: %s", documentID, processingError)
				return nil
			},
		}}

	err := appInstance.processPendingDocumentsBatch(t.Context())
	if err != nil {
		t.Fatalf("process pending documents: %v", err)
	}
	if clearCalls != 1 {
		t.Fatalf("unexpected clearDocumentChunks call count: %d", clearCalls)
	}
	if appendCalls < 2 {
		t.Fatalf("expected multiple appendDocumentChunks calls, got %d", appendCalls)
	}
	if !markCalled {
		t.Fatal("expected markDocumentProcessed to be called")
	}

	for index, chunkRow := range savedChunks {
		if chunkRow.DocumentID != docID {
			t.Fatalf("unexpected chunk document id: %s", chunkRow.DocumentID)
		}
		if chunkRow.Scope != "global" {
			t.Fatalf("unexpected chunk scope: %s", chunkRow.Scope)
		}
		if chunkRow.ChunkIndex != index {
			t.Fatalf("unexpected chunk index: %d", chunkRow.ChunkIndex)
		}
		if strings.TrimSpace(chunkRow.Content) == "" {
			t.Fatal("chunk content should not be empty")
		}
		if len(chunkRow.Embedding) == 0 {
			t.Fatal("chunk embedding should not be empty")
		}
	}

	if embedCallCount != len(savedChunks) {
		t.Fatalf("unexpected embed call count: got %d, want %d", embedCallCount, len(savedChunks))
	}
	if processingTimeSeconds < 0 {
		t.Fatalf("unexpected processing time seconds: %d", processingTimeSeconds)
	}

	if _, statErr := os.Stat(tempFilePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary file should be removed after successful processing, stat err: %v", statErr)
	}
}

func TestProcessPendingDocumentsBatchStoresProcessingError(t *testing.T) {
	docID := uuid.New()
	var capturedError string
	clearCalls := 0

	appInstance := &App{
		embeddingClient: &stubEmbeddingClient{embedFn: func(_ context.Context, _ string) ([]float64, error) {
			return []float64{1}, nil
		}},
		repo: &stubRepository{
			listPendingDocumentsFn: func(_ context.Context, _ int) ([]document, error) {
				return []document{{
					ID:           docID,
					TemporalPath: filepath.Join(t.TempDir(), "missing.txt"),
					Scope:        "global",
				}}, nil
			},
			clearDocumentChunksFn: func(_ context.Context, documentID uuid.UUID) error {
				if documentID != docID {
					t.Fatalf("unexpected document id: %s", documentID)
				}
				clearCalls++
				return nil
			},
			appendDocumentChunksFn: func(_ context.Context, _ []chunk) error {
				t.Fatal("appendDocumentChunks should not be called on open error")
				return nil
			},
			markDocumentProcessedFn: func(_ context.Context, _ uuid.UUID, _ int) error {
				t.Fatal("markDocumentProcessed should not be called on open error")
				return nil
			},
			setDocumentProcessingErrFn: func(_ context.Context, documentID uuid.UUID, processingError string) error {
				if documentID != docID {
					t.Fatalf("unexpected document id: %s", documentID)
				}
				capturedError = processingError
				return nil
			},
		},
	}

	err := appInstance.processPendingDocumentsBatch(t.Context())
	if err == nil {
		t.Fatal("expected processing error")
	}
	if capturedError == "" {
		t.Fatal("expected processing_error to be stored")
	}
	if !strings.Contains(capturedError, "open temporal file") {
		t.Fatalf("unexpected processing_error message: %s", capturedError)
	}
	if clearCalls != 2 {
		t.Fatalf("unexpected clearDocumentChunks call count: got %d, want 2", clearCalls)
	}
}

func TestProcessPendingDocumentsBatchStoresEmbeddingError(t *testing.T) {
	tempDir := t.TempDir()
	tempFilePath := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(tempFilePath, []byte("alpha beta"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	docID := uuid.New()
	var capturedError string
	clearCalls := 0

	appInstance := &App{
		embeddingClient: &stubEmbeddingClient{embedFn: func(_ context.Context, _ string) ([]float64, error) {
			return nil, errors.New("ollama unavailable")
		}},
		repo: &stubRepository{
			listPendingDocumentsFn: func(_ context.Context, _ int) ([]document, error) {
				return []document{{
					ID:           docID,
					TemporalPath: tempFilePath,
					Scope:        "global",
				}}, nil
			},
			clearDocumentChunksFn: func(_ context.Context, documentID uuid.UUID) error {
				if documentID != docID {
					t.Fatalf("unexpected document id: %s", documentID)
				}
				clearCalls++
				return nil
			},
			appendDocumentChunksFn: func(_ context.Context, _ []chunk) error {
				t.Fatal("appendDocumentChunks should not be called on embedding error")
				return nil
			},
			markDocumentProcessedFn: func(_ context.Context, _ uuid.UUID, _ int) error {
				t.Fatal("markDocumentProcessed should not be called on embedding error")
				return nil
			},
			setDocumentProcessingErrFn: func(_ context.Context, documentID uuid.UUID, processingError string) error {
				if documentID != docID {
					t.Fatalf("unexpected document id: %s", documentID)
				}
				capturedError = processingError
				return nil
			},
		},
	}

	err := appInstance.processPendingDocumentsBatch(t.Context())
	if err == nil {
		t.Fatal("expected processing error")
	}
	if !strings.Contains(capturedError, "embed chunk 0") {
		t.Fatalf("unexpected processing_error message: %s", capturedError)
	}
	if clearCalls != 2 {
		t.Fatalf("unexpected clearDocumentChunks call count: got %d, want 2", clearCalls)
	}
}

func TestSplitTextIntoChunksByWhitespace(t *testing.T) {
	content := "alpha beta\ngamma   delta epsilon"

	chunks := splitTextIntoChunks(content, 12)

	if len(chunks) != 3 {
		t.Fatalf("unexpected chunks count: %d", len(chunks))
	}

	expected := []string{"alpha beta", "gamma delta", "epsilon"}
	for i, chunkValue := range chunks {
		if chunkValue != expected[i] {
			t.Fatalf("unexpected chunk[%d]: got %q, want %q", i, chunkValue, expected[i])
		}
	}
}

func TestEmbeddingLatencyTrackerAverage(t *testing.T) {
	tracker := &embeddingLatencyTracker{}
	if _, ok := tracker.average(); ok {
		t.Fatal("expected no average for empty tracker")
	}

	tracker.observe(10 * time.Millisecond)
	tracker.observe(20 * time.Millisecond)
	tracker.observe(30 * time.Millisecond)

	averageLatency, ok := tracker.average()
	if !ok {
		t.Fatal("expected average latency")
	}

	if averageLatency != 20*time.Millisecond {
		t.Fatalf("unexpected average latency: got %v, want %v", averageLatency, 20*time.Millisecond)
	}
}

func TestReportEmbeddingLatencyLoop(t *testing.T) {
	tracker := &embeddingLatencyTracker{}
	tracker.observe(10 * time.Millisecond)
	tracker.observe(30 * time.Millisecond)

	output := &lockedBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		reportEmbeddingLatencyLoop(ctx, tracker, 5*time.Millisecond, output)
		close(done)
	}()

	foundAverage := false
	for range 40 {
		if strings.Contains(output.String(), "embedding request average latency: 20ms") {
			foundAverage = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reportEmbeddingLatencyLoop did not stop after context cancel")
	}

	if !foundAverage {
		t.Fatalf("expected average latency output, got %q", output.String())
	}
}

func TestReportEmbeddingLatencyLoopNoData(t *testing.T) {
	tracker := &embeddingLatencyTracker{}
	output := &lockedBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		reportEmbeddingLatencyLoop(ctx, tracker, 5*time.Millisecond, output)
		close(done)
	}()

	foundNoData := false
	for range 40 {
		if strings.Contains(output.String(), "embedding request average latency: n/a") {
			foundNoData = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reportEmbeddingLatencyLoop did not stop after context cancel")
	}

	if !foundNoData {
		t.Fatalf("expected no-data latency output, got %q", output.String())
	}
}

func TestPersistToTemporaryFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	source, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open sample file: %v", err)
	}
	defer source.Close()

	persistedPath, err := persistToTemporaryFile(tempDir, source)
	if err != nil {
		t.Fatalf("persist file: %v", err)
	}

	content, err := os.ReadFile(persistedPath)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if string(content) != "data" {
		t.Fatalf("unexpected persisted content: %s", string(content))
	}
}
