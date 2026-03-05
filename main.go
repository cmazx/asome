package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	maxUploadSizeBytes    = 100 << 20
	defaultTimeDecayDays  = 365.0
	defaultSemanticWeight = 0.7
	defaultTempWeight     = 0.3
	defaultResultLimit    = 10
	defaultEmbeddingModel = "qwen3-embedding:4b"

	defaultProcessingPollInterval = 2 * time.Second
	defaultProcessingBatchLimit   = 20
	defaultChunkSizeRunes         = 1200
	defaultChunkWriteBatchSize    = 8
	defaultEmbeddingReportPeriod  = 5 * time.Second
)

type config struct {
	dbHost         string
	dbPort         string
	dbUser         string
	dbPassword     string
	dbName         string
	ollamaBaseURL  string
	embeddingModel string
	httpAddr       string
}

type document struct {
	ID              uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	TemporalPath    string     `json:"temporal_path"`
	ProcessingError *string    `json:"processing_error"`
	ProcessingTime  *int       `json:"processing_time"`
	Title           string     `json:"title"`
	Source          string     `json:"source"`
	Scope           string     `json:"scope"`
	DocType         string     `json:"doc_type"`
	CreatedAt       time.Time  `json:"created_at"`
	EffectiveDate   *time.Time `json:"effective_date"`
	ExpiryDate      *time.Time `json:"expiry_date"`
	Version         int        `json:"version"`
	SupersedesID    *uuid.UUID `json:"supersedes_id"`
	Metadata        []byte     `gorm:"type:jsonb" json:"metadata"`
}

func (document) TableName() string {
	return "documents"
}

type chunk struct {
	ID         uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	DocumentID uuid.UUID `gorm:"type:uuid" json:"document_id"`
	ChunkIndex int       `json:"chunk_index"`
	Scope      string    `json:"scope"`
	Content    string    `json:"content"`
	Embedding  []float64 `gorm:"-" json:"embedding"`
}

func (chunk) TableName() string {
	return "chunks"
}

type searchResult struct {
	ChunkID       uuid.UUID `json:"chunk_id"`
	DocumentID    uuid.UUID `json:"document_id"`
	Content       string    `json:"content"`
	Title         string    `json:"title"`
	SemanticScore float64   `json:"semantic_score"`
	TemporalScore float64   `json:"temporal_score"`
	CombinedScore float64   `json:"combined_score"`
}

type temporalSearchParams struct {
	QueryTime      time.Time
	TimeDecayDays  float64
	SemanticWeight float64
	TempWeight     float64
	ResultLimit    int
	FilterDocType  *string
}

type repository interface {
	createDocument(ctx context.Context, doc *document) error
	temporalSearch(ctx context.Context, embedding []float64, params temporalSearchParams) ([]searchResult, error)
	listPendingDocuments(ctx context.Context, limit int) ([]document, error)
	clearDocumentChunks(ctx context.Context, documentID uuid.UUID) error
	appendDocumentChunks(ctx context.Context, chunks []chunk) error
	markDocumentProcessed(ctx context.Context, documentID uuid.UUID, processingTimeSeconds int) error
	setDocumentProcessingError(ctx context.Context, documentID uuid.UUID, processingError string) error
}

type embeddingClient interface {
	embed(ctx context.Context, input string) ([]float64, error)
}

type embeddingLatencyTracker struct {
	totalNanos atomic.Int64
	count      atomic.Int64
}

func (t *embeddingLatencyTracker) observe(latency time.Duration) {
	if latency < 0 {
		return
	}

	t.totalNanos.Add(latency.Nanoseconds())
	t.count.Add(1)
}

func (t *embeddingLatencyTracker) average() (time.Duration, bool) {
	count := t.count.Load()
	if count == 0 {
		return 0, false
	}

	totalNanos := t.totalNanos.Load()
	return time.Duration(totalNanos / count), true
}

type latencyTrackingEmbeddingClient struct {
	next    embeddingClient
	tracker *embeddingLatencyTracker
}

func (c *latencyTrackingEmbeddingClient) embed(ctx context.Context, input string) ([]float64, error) {
	if c.next == nil {
		return nil, errors.New("embedding client is not configured")
	}

	start := time.Now()
	vector, err := c.next.embed(ctx, input)
	if c.tracker != nil {
		c.tracker.observe(time.Since(start))
	}

	return vector, err
}

type gormRepository struct {
	db *gorm.DB
}

func (r *gormRepository) createDocument(ctx context.Context, doc *document) error {
	return r.db.WithContext(ctx).Create(doc).Error
}

func (r *gormRepository) temporalSearch(ctx context.Context, embedding []float64, params temporalSearchParams) ([]searchResult, error) {
	vectorValue := formatVector(embedding)
	filterDocType := any(nil)
	if params.FilterDocType != nil {
		filterDocType = *params.FilterDocType
	}

	var results []searchResult
	err := r.db.WithContext(ctx).Raw(`
		SELECT chunk_id, document_id, content, title, semantic_score, temporal_score, combined_score
		FROM temporal_search(?::vector, ?, ?, ?, ?, ?, ?)
	`, vectorValue, params.QueryTime, params.TimeDecayDays, params.SemanticWeight, params.TempWeight, params.ResultLimit, filterDocType).Scan(&results).Error
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (r *gormRepository) listPendingDocuments(ctx context.Context, limit int) ([]document, error) {
	var docs []document
	err := r.db.WithContext(ctx).
		Where("temporal_path IS NOT NULL AND temporal_path <> ''").
		Where("processing_error IS NULL").
		Order("created_at ASC").
		Limit(limit).
		Find(&docs).Error
	if err != nil {
		return nil, err
	}

	return docs, nil
}

func (r *gormRepository) clearDocumentChunks(ctx context.Context, documentID uuid.UUID) error {
	return r.db.WithContext(ctx).Where("document_id = ?", documentID).Delete(&chunk{}).Error
}

func (r *gormRepository) appendDocumentChunks(ctx context.Context, chunks []chunk) error {
	for _, row := range chunks {
		if len(row.Embedding) == 0 {
			return errors.New("chunk embedding is empty")
		}

		if err := r.db.WithContext(ctx).Exec(`
			INSERT INTO chunks (document_id, chunk_index, scope, content, embedding)
			VALUES (?, ?, ?, ?, ?::vector)
		`, row.DocumentID, row.ChunkIndex, row.Scope, row.Content, formatVector(row.Embedding)).Error; err != nil {
			return err
		}
	}

	return nil
}

func (r *gormRepository) markDocumentProcessed(ctx context.Context, documentID uuid.UUID, processingTimeSeconds int) error {
	return r.db.WithContext(ctx).
		Model(&document{}).
		Where("id = ?", documentID).
		Updates(map[string]any{
			"temporal_path":    "",
			"processing_error": nil,
			"processing_time":  processingTimeSeconds,
		}).Error
}

func (r *gormRepository) setDocumentProcessingError(ctx context.Context, documentID uuid.UUID, processingError string) error {
	return r.db.WithContext(ctx).
		Model(&document{}).
		Where("id = ?", documentID).
		Update("processing_error", processingError).Error
}

type ollamaEmbeddingClient struct {
	httpClient *http.Client
	baseURL    string
	model      string
}

func (c *ollamaEmbeddingClient) embed(ctx context.Context, input string) ([]float64, error) {
	payload := map[string]any{
		"model": "trollathon/bge-m3-safetensors", // c.model,
		"input": []string{input},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	url := strings.TrimRight(c.baseURL, "/") + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform embed request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("failed to close embed response body: %v", closeErr)
		}
	}()

	if resp.StatusCode >= http.StatusBadRequest {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("embed request failed with status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("embed request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var embedResponse struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&embedResponse); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	if len(embedResponse.Data) > 0 && len(embedResponse.Data[0].Embedding) > 0 {
		return embedResponse.Data[0].Embedding, nil
	}

	return nil, errors.New("ollama response does not contain embedding")
}

type App struct {
	repo                   repository
	embeddingClient        embeddingClient
	embeddingLatencyMetric *embeddingLatencyTracker
	tempDir                string
	now                    func() time.Time
}

func (a *App) startBackgroundProcessor(ctx context.Context) {
	go a.processPendingDocumentsLoop(ctx)
}

func (a *App) startEmbeddingLatencyReporter(ctx context.Context) {
	if a.embeddingLatencyMetric == nil {
		return
	}

	go reportEmbeddingLatencyLoop(ctx, a.embeddingLatencyMetric, defaultEmbeddingReportPeriod, os.Stdout)
}

func reportEmbeddingLatencyLoop(ctx context.Context, tracker *embeddingLatencyTracker, reportPeriod time.Duration, out io.Writer) {
	if tracker == nil || out == nil {
		return
	}

	ticker := time.NewTicker(reportPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			averageLatency, ok := tracker.average()
			if !ok {
				_, _ = fmt.Fprintln(out, "embedding request average latency: n/a")
				continue
			}

			_, _ = fmt.Fprintf(out, "embedding request average latency: %s\n", averageLatency)
		}
	}
}

func (a *App) processPendingDocumentsLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultProcessingPollInterval)
	defer ticker.Stop()
	go func() {
		if os.Getenv("FILE_READ") != "true" {
			return
		}
		readTarFile(a.createDocument, a.processDocumentReader)
	}()
	for {
		if err := a.processPendingDocumentsBatch(ctx); err != nil {
			log.Printf("background document processing finished with errors: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) processPendingDocumentsBatch(ctx context.Context) error {
	docs, err := a.repo.listPendingDocuments(ctx, defaultProcessingBatchLimit)
	if err != nil {
		return fmt.Errorf("list pending documents: %w", err)
	}

	var processingErr error
	for _, doc := range docs {
		if err = a.processDocument(ctx, doc); err != nil {
			processingErr = errors.Join(processingErr, fmt.Errorf("process document %s: %w", doc.ID, err))
		}
	}

	return processingErr
}

func (a *App) processDocument(ctx context.Context, doc document) error {
	processingStartedAt := time.Now()

	if err := a.repo.clearDocumentChunks(ctx, doc.ID); err != nil {
		return a.storeDocumentProcessingError(ctx, doc.ID, err)
	}

	file, err := os.Open(doc.TemporalPath)
	if err != nil {
		return fmt.Errorf("open temporal file %s: %w", doc.TemporalPath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("failed to close temporal file %s: %v", doc.TemporalPath, closeErr)
		}
	}()
	if err := a.processDocumentChunksFromFile(ctx, file, &doc); err != nil {
		return a.storeDocumentProcessingErrorWithChunkCleanup(ctx, doc.ID, err)
	}

	if err := os.Remove(doc.TemporalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("failed to remove processed temporary file %s: %v", doc.TemporalPath, err)
	}

	processingTimeSeconds := int(time.Since(processingStartedAt).Seconds())
	if err := a.repo.markDocumentProcessed(ctx, doc.ID, processingTimeSeconds); err != nil {
		return a.storeDocumentProcessingErrorWithChunkCleanup(ctx, doc.ID, err)
	}

	return nil
}

func (a *App) createDocument(name string) (*document, error) {
	doc := &document{
		Title: name,
	}
	if err := a.repo.createDocument(context.Background(), doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (a *App) processDocumentReader(ctx context.Context, doc *document, fileReader io.Reader) error {
	processingStartedAt := time.Now()
	fmt.Println("file: ", doc.Title)
	if err := a.processDocumentChunksFromFile(ctx, fileReader, doc); err != nil {
		return a.storeDocumentProcessingErrorWithChunkCleanup(ctx, doc.ID, err)
	}

	processingTimeSeconds := int(time.Since(processingStartedAt).Seconds())
	if err := a.repo.markDocumentProcessed(ctx, doc.ID, processingTimeSeconds); err != nil {
		return a.storeDocumentProcessingErrorWithChunkCleanup(ctx, doc.ID, err)
	}

	return nil
}

func (a *App) storeDocumentProcessingErrorWithChunkCleanup(ctx context.Context, documentID uuid.UUID, sourceErr error) error {
	if err := a.repo.clearDocumentChunks(ctx, documentID); err != nil {
		sourceErr = errors.Join(sourceErr, fmt.Errorf("clear document chunks: %w", err))
	}

	return a.storeDocumentProcessingError(ctx, documentID, sourceErr)
}

func (a *App) storeDocumentProcessingError(ctx context.Context, documentID uuid.UUID, sourceErr error) error {
	message := sourceErr.Error()
	if err := a.repo.setDocumentProcessingError(ctx, documentID, message); err != nil {
		return errors.Join(sourceErr, fmt.Errorf("set processing_error: %w", err))
	}

	return sourceErr
}

func (a *App) processDocumentChunksFromFile(ctx context.Context, file io.Reader, doc *document) error {
	if a.embeddingClient == nil {
		return errors.New("embedding client is not configured")
	}

	batchSize := max(defaultChunkWriteBatchSize, 1)
	chunkBatch := make([]chunk, 0, batchSize)
	chunkIndex := 0
	hasContent := false

	flushBatch := func() error {
		if len(chunkBatch) == 0 {
			return nil
		}

		if appendErr := a.repo.appendDocumentChunks(ctx, chunkBatch); appendErr != nil {
			return fmt.Errorf("append chunk batch: %w", appendErr)
		}

		chunkBatch = chunkBatch[:0]
		return nil
	}

	emitChunk := func(chunkContent string) error {
		hasContent = true

		embedding, embeddingErr := a.embeddingClient.embed(ctx, chunkContent)
		if embeddingErr != nil {
			return fmt.Errorf("embed chunk %d: %w", chunkIndex, embeddingErr)
		}
		if len(embedding) == 0 {
			return fmt.Errorf("embed chunk %d: empty embedding", chunkIndex)
		}

		chunkBatch = append(chunkBatch, chunk{
			DocumentID: doc.ID,
			ChunkIndex: chunkIndex,
			Scope:      doc.Scope,
			Content:    chunkContent,
			Embedding:  embedding,
		})
		chunkIndex++

		if len(chunkBatch) >= batchSize {
			return flushBatch()
		}

		return nil
	}

	if err := streamTextChunks(file, defaultChunkSizeRunes, emitChunk); err != nil {
		return fmt.Errorf("stream temporal file %s: %w", doc.TemporalPath, err)
	}
	if !hasContent {
		return errors.New("document content is empty")
	}

	if err := flushBatch(); err != nil {
		return err
	}

	return nil
}

func streamTextChunks(reader io.Reader, chunkSizeRunes int, emitChunk func(string) error) error {
	if emitChunk == nil {
		return errors.New("emit chunk function is nil")
	}

	chunkSizeRunes = max(chunkSizeRunes, 1)
	bufferedReader := bufio.NewReader(reader)

	var currentChunk strings.Builder
	currentChunkLength := 0

	var tokenBuilder strings.Builder
	tokenLength := 0
	tokenWasSplit := false

	flushCurrentChunk := func() error {
		chunkValue := strings.TrimSpace(currentChunk.String())
		currentChunk.Reset()
		currentChunkLength = 0
		if chunkValue == "" {
			return nil
		}

		return emitChunk(chunkValue)
	}

	flushToken := func() error {
		if tokenLength == 0 {
			tokenWasSplit = false
			return nil
		}

		tokenValue := tokenBuilder.String()
		tokenBuilder.Reset()

		if tokenWasSplit {
			if err := flushCurrentChunk(); err != nil {
				return err
			}
			if err := emitChunk(tokenValue); err != nil {
				return err
			}
			tokenLength = 0
			tokenWasSplit = false
			return nil
		}

		if currentChunkLength == 0 {
			currentChunk.WriteString(tokenValue)
			currentChunkLength = tokenLength
			tokenLength = 0
			return nil
		}

		if currentChunkLength+1+tokenLength <= chunkSizeRunes {
			currentChunk.WriteByte(' ')
			currentChunk.WriteString(tokenValue)
			currentChunkLength += 1 + tokenLength
			tokenLength = 0
			tokenWasSplit = false
			return nil
		}

		if err := flushCurrentChunk(); err != nil {
			return err
		}

		currentChunk.WriteString(tokenValue)
		currentChunkLength = tokenLength
		tokenLength = 0
		tokenWasSplit = false
		return nil
	}

	for {
		r, _, readErr := bufferedReader.ReadRune()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}

		if unicode.IsSpace(r) {
			if err := flushToken(); err != nil {
				return err
			}
			continue
		}

		tokenBuilder.WriteRune(r)
		tokenLength++
		if tokenLength < chunkSizeRunes {
			continue
		}

		if err := flushCurrentChunk(); err != nil {
			return err
		}
		if err := emitChunk(tokenBuilder.String()); err != nil {
			return err
		}

		tokenBuilder.Reset()
		tokenLength = 0
		tokenWasSplit = true
	}

	if err := flushToken(); err != nil {
		return err
	}

	return flushCurrentChunk()
}

func splitTextIntoChunks(content string, chunkSizeRunes int) []string {
	chunkSizeRunes = max(chunkSizeRunes, 1)

	chunks := make([]string, 0, utf8.RuneCountInString(content)/chunkSizeRunes+1)
	if err := streamTextChunks(strings.NewReader(content), chunkSizeRunes, func(chunkContent string) error {
		chunks = append(chunks, chunkContent)
		return nil
	}); err != nil {
		return nil
	}

	return chunks
}

type uploadResponse struct {
	ID           uuid.UUID `json:"id"`
	TemporalPath string    `json:"temporal_path"`
}

type searchRequest struct {
	Query          string     `json:"query"`
	QueryTime      *time.Time `json:"query_time,omitzero"`
	TimeDecayDays  *float64   `json:"time_decay_days,omitzero"`
	SemanticWeight *float64   `json:"semantic_weight,omitzero"`
	TempWeight     *float64   `json:"temp_weight,omitzero"`
	ResultLimit    *int       `json:"result_limit,omitzero"`
	FilterDocType  *string    `json:"filter_doc_type,omitzero"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

func (a *App) uploadDocumentHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSizeBytes)
	if err := r.ParseMultipartForm(maxUploadSizeBytes); err != nil {
		respondError(w, http.StatusBadRequest, "invalid multipart form data")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("failed to close uploaded file: %v", closeErr)
		}
	}()

	if filepath.Ext(fileHeader.Filename) != ".txt" {
		respondError(w, http.StatusBadRequest, "only .txt files are allowed")
		return
	}

	scope := strings.TrimSpace(r.FormValue("scope"))
	if scope == "" {
		respondError(w, http.StatusBadRequest, "scope is required")
		return
	}

	source := strings.TrimSpace(r.FormValue("source"))
	docType := strings.TrimSpace(r.FormValue("doc_type"))
	version, err := parseVersion(r.FormValue("version"))
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	tempFilePath, err := persistToTemporaryFile(a.tempDir, file)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to persist uploaded file")
		return
	}

	doc := &document{
		TemporalPath: tempFilePath,
		Title:        fileHeader.Filename,
		Source:       source,
		Scope:        scope,
		DocType:      docType,
		Version:      version,
		Metadata:     []byte("{}"),
	}
	if err = a.repo.createDocument(r.Context(), doc); err != nil {
		_ = os.Remove(tempFilePath)
		respondError(w, http.StatusInternalServerError, "failed to create document")
		return
	}

	respondJSON(w, http.StatusCreated, uploadResponse{
		ID:           doc.ID,
		TemporalPath: tempFilePath,
	})
}

func (a *App) searchHandler(w http.ResponseWriter, r *http.Request) {
	var searchReq searchRequest
	if err := json.NewDecoder(r.Body).Decode(&searchReq); err != nil {
		respondError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	query := strings.TrimSpace(searchReq.Query)
	if query == "" {
		respondError(w, http.StatusBadRequest, "query is required")
		return
	}

	params, err := buildTemporalSearchParams(searchReq, a.now())
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	embedding, err := a.embeddingClient.embed(r.Context(), query)
	if err != nil {
		respondError(w, http.StatusBadGateway, "failed to get query embedding")
		return
	}

	results, err := a.repo.temporalSearch(r.Context(), embedding, params)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "search query failed")
		return
	}

	respondJSON(w, http.StatusOK, searchResponse{Results: results})
}

func parseVersion(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 1, nil
	}

	version, err := strconv.Atoi(trimmed)
	if err != nil || version <= 0 {
		return 0, errors.New("version must be a positive integer")
	}

	return version, nil
}

func persistToTemporaryFile(tempDir string, source multipart.File) (string, error) {
	tempFile, err := os.CreateTemp(tempDir, "asome-*.txt")
	if err != nil {
		return "", err
	}

	_, copyErr := io.Copy(tempFile, source)
	closeErr := tempFile.Close()
	if copyErr != nil {
		_ = os.Remove(tempFile.Name())
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tempFile.Name())
		return "", closeErr
	}

	return tempFile.Name(), nil
}

func buildTemporalSearchParams(searchReq searchRequest, currentTime time.Time) (temporalSearchParams, error) {
	params := temporalSearchParams{
		QueryTime:      currentTime,
		TimeDecayDays:  defaultTimeDecayDays,
		SemanticWeight: defaultSemanticWeight,
		TempWeight:     defaultTempWeight,
		ResultLimit:    defaultResultLimit,
	}

	if searchReq.QueryTime != nil {
		params.QueryTime = searchReq.QueryTime.UTC()
	}
	if searchReq.TimeDecayDays != nil {
		if *searchReq.TimeDecayDays <= 0 {
			return temporalSearchParams{}, errors.New("time_decay_days must be positive")
		}
		params.TimeDecayDays = *searchReq.TimeDecayDays
	}
	if searchReq.SemanticWeight != nil {
		if *searchReq.SemanticWeight < 0 || *searchReq.SemanticWeight > 1 {
			return temporalSearchParams{}, errors.New("semantic_weight must be between 0 and 1")
		}
		params.SemanticWeight = *searchReq.SemanticWeight
	}
	if searchReq.TempWeight != nil {
		if *searchReq.TempWeight < 0 || *searchReq.TempWeight > 1 {
			return temporalSearchParams{}, errors.New("temp_weight must be between 0 and 1")
		}
		params.TempWeight = *searchReq.TempWeight
	}
	if searchReq.ResultLimit != nil {
		if *searchReq.ResultLimit <= 0 {
			return temporalSearchParams{}, errors.New("result_limit must be positive")
		}
		params.ResultLimit = *searchReq.ResultLimit
	}
	if searchReq.FilterDocType != nil {
		docType := strings.TrimSpace(*searchReq.FilterDocType)
		if docType != "" {
			params.FilterDocType = &docType
		}
	}

	return params, nil
}

func formatVector(values []float64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatFloat(value, 'f', -1, 64))
	}

	return "[" + strings.Join(parts, ",") + "]"
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to encode json response: %v", err)
	}
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func loadConfig() config {
	return config{
		dbHost:         envOrDefault("DB_HOST", "localhost"),
		dbPort:         envOrDefault("DB_PORT", "5432"),
		dbUser:         envOrDefault("DB_USER", "asome"),
		dbPassword:     envOrDefault("DB_PASSWORD", "asome"),
		dbName:         envOrDefault("DB_NAME", "asome"),
		ollamaBaseURL:  envOrDefault("OLLAMA_BASE_URL", "http://localhost:8881"),
		embeddingModel: envOrDefault("OLLAMA_EMBEDDING_MODEL", defaultEmbeddingModel),
		httpAddr:       envOrDefault("HTTP_ADDR", ":8880"),
	}
}

func envOrDefault(key, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}

	return value
}

func main() {
	conf := loadConfig()

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		conf.dbHost,
		conf.dbPort,
		conf.dbUser,
		conf.dbPassword,
		conf.dbName,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}

	baseEmbeddingClient := &ollamaEmbeddingClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    conf.ollamaBaseURL,
		model:      conf.embeddingModel,
	}
	latencyTracker := &embeddingLatencyTracker{}

	application := &App{
		repo: &gormRepository{db: db},
		embeddingClient: &latencyTrackingEmbeddingClient{
			next:    baseEmbeddingClient,
			tracker: latencyTracker,
		},
		embeddingLatencyMetric: latencyTracker,
		tempDir:                os.TempDir(),
		now:                    time.Now,
	}

	appContext := context.Background()
	application.startEmbeddingLatencyReporter(appContext)
	application.startBackgroundProcessor(appContext)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /documents", application.uploadDocumentHandler)
	mux.HandleFunc("POST /search", application.searchHandler)

	server := &http.Server{
		Addr:         conf.httpAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("server started on %s", conf.httpAddr)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server failed: %v", err)
	}
}
