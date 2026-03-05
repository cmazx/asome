-- Расширение для векторного поиска
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm; -- для fuzzy text search

-- Основная таблица документов
CREATE TABLE documents (
                           id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                           temporal_path TEXT,
                           processing_error TEXT,
                           processing_time int,
                           title TEXT NOT NULL,
                           source TEXT,              -- файл, URL, система
                           scope VARCHAR(255) NOT NULL,
                           doc_type TEXT,            -- договор, приказ, отчёт...
                           created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                           effective_date TIMESTAMPTZ,   -- дата вступления в силу
                           expiry_date TIMESTAMPTZ,      -- дата окончания действия
                           version INT DEFAULT 1,
                           supersedes_id UUID REFERENCES documents(id),
                           metadata JSONB DEFAULT '{}'
);

-- Чанки с эмбеддингами
drop table chunks;
CREATE TABLE chunks (
                        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                        document_id UUID REFERENCES documents(id) ON DELETE CASCADE,
                        chunk_index INT NOT NULL,
                        scope VARCHAR(255) NOT NULL,
                        content TEXT NOT NULL,
                        embedding halfvec(1024),     
                        token_count INT,
                        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                        temporal_weight FLOAT DEFAULT 1.0  -- вес "свежести"
);

ALTER EXTENSION vector UPDATE;

-- Индексы
CREATE INDEX idx_chunks_embedding ON chunks
    USING ivfflat (embedding halfvec_cosine_ops) WITH (lists = 100);

CREATE INDEX idx_chunks_doc_id ON chunks(document_id);
CREATE INDEX idx_docs_dates ON documents(effective_date, expiry_date);
CREATE INDEX idx_docs_type ON documents(doc_type);
CREATE INDEX idx_docs_metadata ON documents USING GIN(metadata);
CREATE INDEX idx_docs_chunks ON chunks(scope);
CREATE INDEX idx_chunks_content_trgm ON chunks USING GIN(content gin_trgm_ops);

-- Функция: комбинированный поиск (семантика + время)
CREATE OR REPLACE FUNCTION temporal_search(
    query_embedding halfvec(1024),
    query_time TIMESTAMPTZ DEFAULT now(),
    time_decay_days FLOAT DEFAULT 365.0,
    semantic_weight FLOAT DEFAULT 0.7,
    temp_weight FLOAT DEFAULT 0.3,
    result_limit INT DEFAULT 10,
    filter_doc_type TEXT DEFAULT NULL
)
    RETURNS TABLE(
                     chunk_id UUID,
                     document_id UUID,
                     content TEXT,
                     title TEXT,
                     semantic_score FLOAT,
                     temporal_score FLOAT,
                     combined_score FLOAT
                 ) AS $$
BEGIN
    RETURN QUERY
        SELECT
            c.id AS chunk_id,
            d.id AS document_id,
            c.content,
            d.title,
            -- Семантический скор (cosine similarity)
            (1 - (c.embedding <=> query_embedding))::FLOAT AS semantic_score,
            -- Временной скор (экспоненциальное затухание)
            EXP(
                    -EXTRACT(EPOCH FROM (query_time - COALESCE(d.effective_date, d.created_at)))
                        / (time_decay_days * 86400)
            )::FLOAT AS temporal_score,
            -- Комбинированный скор
            (
                semantic_weight * (1 - (c.embedding <=> query_embedding)) +
                temp_weight * EXP(
                        -EXTRACT(EPOCH FROM (query_time - COALESCE(d.effective_date, d.created_at)))
                            / (time_decay_days * 86400)
                              )
                )::FLOAT AS combined_score
        FROM chunks c
                 JOIN documents d ON c.document_id = d.id
        WHERE
            (filter_doc_type IS NULL OR d.doc_type = filter_doc_type)
          AND (d.expiry_date IS NULL OR d.expiry_date > query_time)
        ORDER BY combined_score DESC
        LIMIT result_limit;
END;
$$ LANGUAGE plpgsql;