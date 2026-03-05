import { FormEvent, useEffect, useMemo, useState } from 'react'
import {
  defaultSearchModifiers,
  fetchRecentDocuments,
  RecentDocument,
  search,
  SearchResult,
  uploadDocument,
  validateUploadFile,
} from './api'

const floatFormat = new Intl.NumberFormat('ru-RU', {
  maximumFractionDigits: 4,
})

export function App() {
  const [file, setFile] = useState<File | null>(null)
  const [scope, setScope] = useState('')
  const [source, setSource] = useState('')
  const [docType, setDocType] = useState('')
  const [version, setVersion] = useState('1')
  const [isUploadSectionOpen, setIsUploadSectionOpen] = useState(false)
  const [uploadMessage, setUploadMessage] = useState<string | null>(null)
  const [uploadError, setUploadError] = useState<string | null>(null)
  const [isUploading, setIsUploading] = useState(false)
  const [isRecentDocumentsSectionOpen, setIsRecentDocumentsSectionOpen] = useState(false)
  const [recentDocuments, setRecentDocuments] = useState<RecentDocument[]>([])
  const [recentDocumentsError, setRecentDocumentsError] = useState<string | null>(null)
  const [isRecentDocumentsLoading, setIsRecentDocumentsLoading] = useState(true)

  const [query, setQuery] = useState('')
  const [filterScope, setFilterScope] = useState('')
  const [timeDecayDays, setTimeDecayDays] = useState<number>(defaultSearchModifiers.timeDecayDays)
  const [semanticWeight, setSemanticWeight] = useState<number>(defaultSearchModifiers.semanticWeight)
  const [fulltextWeight, setFulltextWeight] = useState<number>(defaultSearchModifiers.fulltextWeight)
  const [tempWeight, setTempWeight] = useState<number>(defaultSearchModifiers.tempWeight)
  const [resultLimit, setResultLimit] = useState<number>(defaultSearchModifiers.resultLimit)
  const [results, setResults] = useState<SearchResult[]>([])
  const [searchError, setSearchError] = useState<string | null>(null)
  const [isSearching, setIsSearching] = useState(false)

  const totalWeight = useMemo(
    () => semanticWeight + fulltextWeight + tempWeight,
    [semanticWeight, fulltextWeight, tempWeight],
  )

  async function loadRecentDocuments() {
    setRecentDocumentsError(null)
    setIsRecentDocumentsLoading(true)
    try {
      const recent = await fetchRecentDocuments()
      setRecentDocuments(recent)
    } catch (error) {
      setRecentDocumentsError(error instanceof Error ? error.message : 'Не удалось получить последние документы')
    } finally {
      setIsRecentDocumentsLoading(false)
    }
  }

  function recentDocumentStatus(document: RecentDocument): string {
    const processingError = document.processing_error?.trim()
    if (processingError != null && processingError !== '') {
      return `ошибка: ${processingError}`
    }
    if (document.processing_time != null) {
      return `processing time: ${document.processing_time} c`
    }

    return 'в обработке'
  }

  useEffect(() => {
    void loadRecentDocuments()
  }, [])

  async function handleUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setUploadError(null)
    setUploadMessage(null)

    const fileError = validateUploadFile(file)
    if (fileError != null) {
      setUploadError(fileError)
      return
    }

    const trimmedScope = scope.trim()
    if (trimmedScope === '') {
      setUploadError('Поле scope обязательно')
      return
    }

    const parsedVersion = Number.parseInt(version, 10)
    if (!Number.isFinite(parsedVersion) || parsedVersion <= 0) {
      setUploadError('Version должен быть положительным целым числом')
      return
    }

    setIsUploading(true)
    try {
      const uploaded = await uploadDocument({
        file: file!,
        scope: trimmedScope,
        source,
        docType,
        version: parsedVersion,
      })
      await loadRecentDocuments()
      setUploadMessage(`Документ загружен: ${uploaded.id}`)
    } catch (error) {
      setUploadError(error instanceof Error ? error.message : 'Не удалось загрузить документ')
    } finally {
      setIsUploading(false)
    }
  }

  async function handleSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSearchError(null)
    setResults([])

    if (query.trim() === '') {
      setSearchError('Введите поисковый запрос')
      return
    }

    setIsSearching(true)
    try {
      const found = await search({
        query,
        filterScope,
        timeDecayDays,
        semanticWeight,
        fulltextWeight,
        tempWeight,
        resultLimit,
      })
      setResults(found)
    } catch (error) {
      setSearchError(error instanceof Error ? error.message : 'Не удалось выполнить поиск')
    } finally {
      setIsSearching(false)
    }
  }

  return (
    <div className="app-shell">
      <header className="hero">
        <span className="brand">[A]some</span>
        <h1>Поиск</h1>
      </header>

      <main className="layout">
        <section className="card">
          <div className="card-header">
            <h2>Загрузка .txt документа</h2>
            <button
              type="button"
              className="card-toggle"
              onClick={() => setIsUploadSectionOpen((isOpen) => !isOpen)}
            >
              {isUploadSectionOpen ? 'Свернуть' : 'Развернуть'}
            </button>
          </div>

          {isUploadSectionOpen ? (
            <div className="card-content">
              <form onSubmit={handleUpload} className="form-grid">
                <label>
                  Файл (`.txt`, до 100 МБ)
                  <input
                    type="file"
                    accept=".txt,text/plain"
                    onChange={(event) => setFile(event.target.files?.[0] ?? null)}
                  />
                </label>

                <label>
                  Scope *
                  <input value={scope} onChange={(event) => setScope(event.target.value)} required />
                </label>

                <div className="row two-cols">
                  <label>
                    Source
                    <input value={source} onChange={(event) => setSource(event.target.value)} />
                  </label>
                  <label>
                    Doc type
                    <input value={docType} onChange={(event) => setDocType(event.target.value)} />
                  </label>
                </div>

                <label>
                  Version
                  <input
                    type="number"
                    min={1}
                    step={1}
                    value={version}
                    onChange={(event) => setVersion(event.target.value)}
                  />
                </label>

                <button type="submit" disabled={isUploading}>
                  {isUploading ? 'Загрузка...' : 'Загрузить документ'}
                </button>
              </form>
              {uploadError != null ? <p className="message error">{uploadError}</p> : null}
              {uploadMessage != null ? <p className="message success">{uploadMessage}</p> : null}
            </div>
          ) : null}
        </section>

        <section className="card">
          <div className="card-header">
            <h2>Последние 5 документов</h2>
            <button
              type="button"
              className="card-toggle"
              onClick={() => setIsRecentDocumentsSectionOpen((isOpen) => !isOpen)}
            >
              {isRecentDocumentsSectionOpen ? 'Свернуть' : 'Развернуть'}
            </button>
          </div>

          {isRecentDocumentsSectionOpen ? (
            <div className="card-content">
              {isRecentDocumentsLoading ? (
                <p className="empty">Загрузка...</p>
              ) : recentDocumentsError != null ? (
                <p className="message error">{recentDocumentsError}</p>
              ) : recentDocuments.length === 0 ? (
                <p className="empty">Документы пока отсутствуют.</p>
              ) : (
                <ul className="recent-documents">
                  {recentDocuments.map((document, index) => (
                    <li key={`${document.title}-${index}`} className="recent-document-item">
                      {document.title} — {recentDocumentStatus(document)}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          ) : null}
        </section>

        <section className="card">
          <h2>Поиск</h2>
          <form onSubmit={handleSearch} className="form-grid">
            <label>
              Запрос *
              <textarea rows={4} value={query} onChange={(event) => setQuery(event.target.value)} required />
            </label>

            <label>
              Фильтр по `scope`
              <input
                value={filterScope}
                onChange={(event) => setFilterScope(event.target.value)}
                placeholder="Например: hr"
              />
            </label>

            <div className="slider-grid">
              <Slider
                label="Time decay days"
                min={1}
                max={2000}
                step={1}
                value={timeDecayDays}
                onChange={setTimeDecayDays}
              />
              <Slider
                label="Semantic weight"
                min={0}
                max={1}
                step={0.01}
                value={semanticWeight}
                onChange={setSemanticWeight}
              />
              <Slider
                label="Fulltext weight"
                min={0}
                max={1}
                step={0.01}
                value={fulltextWeight}
                onChange={setFulltextWeight}
              />
              <Slider
                label="Temporal weight"
                min={0}
                max={1}
                step={0.01}
                value={tempWeight}
                onChange={setTempWeight}
              />
              <Slider
                label="Result limit"
                min={1}
                max={50}
                step={1}
                value={resultLimit}
                onChange={setResultLimit}
              />
            </div>

            <div className="hint">Сумма весов: {floatFormat.format(totalWeight)}</div>

            <button type="submit" disabled={isSearching}>
              {isSearching ? 'Поиск...' : 'Найти'}
            </button>
          </form>
          {searchError != null ? <p className="message error">{searchError}</p> : null}

          <div className="results">
            {results.length === 0 ? (
              <p className="empty">Результаты появятся здесь.</p>
            ) : (
              results.map((result) => (
                <article key={result.chunk_id} className="result-card">
                  <div className="result-meta">
                    <span>doc_id: {result.document_id}</span>
                    <strong>score: {floatFormat.format(result.combined_score)}</strong>
                  </div>
                  <p>{result.content}</p>
                </article>
              ))
            )}
          </div>
        </section>
      </main>
    </div>
  )
}

type SliderProps = {
  label: string
  min: number
  max: number
  step: number
  value: number
  onChange: (value: number) => void
}

function Slider(props: SliderProps) {
  return (
    <label className="slider">
      <span>{props.label}</span>
      <div className="slider-row">
        <input
          type="range"
          min={props.min}
          max={props.max}
          step={props.step}
          value={props.value}
          onChange={(event) => props.onChange(Number.parseFloat(event.target.value))}
        />
        <strong>{props.value}</strong>
      </div>
    </label>
  )
}