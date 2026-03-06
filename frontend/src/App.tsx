import { FormEvent, ReactNode, useEffect, useState } from 'react'
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

const searchPageHash = '#/'
const documentsPageHash = '#/documents'

type Page = 'search' | 'documents'

function pageFromHash(hash: string): Page {
  if (hash.trim().toLowerCase() === documentsPageHash) {
    return 'documents'
  }

  return 'search'
}

function isWordCharacter(value: string): boolean {
  return /[\p{L}\p{N}_]/u.test(value)
}

export function highlightExactMatch(content: string, query: string): ReactNode {
  const searchTerm = query.trim()
  if (searchTerm === '') {
    return content
  }

  const normalizedContent = content.toLocaleLowerCase()
  const normalizedTerm = searchTerm.toLocaleLowerCase()

  const fragments: ReactNode[] = []
  let cursor = 0
  let fromIndex = 0

  while (true) {
    const matchStart = normalizedContent.indexOf(normalizedTerm, fromIndex)
    if (matchStart < 0) {
      break
    }

    const matchEnd = matchStart + normalizedTerm.length
    const startBoundary = matchStart === 0 || !isWordCharacter(content[matchStart - 1])
    const endBoundary = matchEnd === content.length || !isWordCharacter(content[matchEnd])

    if (startBoundary && endBoundary) {
      if (matchStart > cursor) {
        fragments.push(content.slice(cursor, matchStart))
      }

      fragments.push(
        <mark key={`${matchStart}-${matchEnd}`} className="search-highlight">
          {content.slice(matchStart, matchEnd)}
        </mark>,
      )
      cursor = matchEnd
    }

    fromIndex = matchEnd
  }

  if (fragments.length === 0) {
    return content
  }

  if (cursor < content.length) {
    fragments.push(content.slice(cursor))
  }

  return <>{fragments}</>
}

export function App() {
  const [page, setPage] = useState<Page>(() => pageFromHash(window.location.hash))
  const [file, setFile] = useState<File | null>(null)
  const [scope, setScope] = useState('')
  const [source, setSource] = useState('')
  const [docType, setDocType] = useState('')
  const [version, setVersion] = useState('1')
  const [uploadMessage, setUploadMessage] = useState<string | null>(null)
  const [uploadError, setUploadError] = useState<string | null>(null)
  const [isUploading, setIsUploading] = useState(false)
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

  async function loadRecentDocuments(showLoading = true) {
    setRecentDocumentsError(null)
    if (showLoading) {
      setIsRecentDocumentsLoading(true)
    }

    try {
      const recent = await fetchRecentDocuments()
      setRecentDocuments(recent)
    } catch (error) {
      setRecentDocumentsError(error instanceof Error ? error.message : 'Не удалось получить последние документы')
    } finally {
      setIsRecentDocumentsLoading(false)
    }
  }

  function recentDocumentLine(document: RecentDocument): string {
    if (document.processing_time != null) {
      return `${document.title} обработан за ${document.processing_time} секунд`
    }

    const processingError = document.processing_error?.trim()
    if (processingError != null && processingError !== '') {
      return `${document.title} — ошибка: ${processingError}`
    }

    return `${document.title} — в обработке`
  }

  useEffect(() => {
    const syncPageFromHash = () => {
      setPage(pageFromHash(window.location.hash))
    }

    window.addEventListener('hashchange', syncPageFromHash)

    return () => {
      window.removeEventListener('hashchange', syncPageFromHash)
    }
  }, [])

  useEffect(() => {
    if (page === 'documents') {
      void loadRecentDocuments()

      const intervalId = window.setInterval(() => {
        void loadRecentDocuments(false)
      }, 3000)

      return () => {
        window.clearInterval(intervalId)
      }
    }
  }, [page])

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
      await loadRecentDocuments(false)
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

  const pageTitle = page === 'search' ? 'Поиск' : 'Документы'

  return (
    <div className="app-shell">
      <header className="hero">
        <div className="hero-top">
          <span className="brand">[A]some</span>
          <nav className="hero-nav" aria-label="Основная навигация">
            <a href={searchPageHash} className={`hero-link ${page === 'search' ? 'active' : ''}`.trim()}>
              Поиск
            </a>
            <a href={documentsPageHash} className={`hero-link ${page === 'documents' ? 'active' : ''}`.trim()}>
              Документы
            </a>
          </nav>
        </div>
        <h1>{pageTitle}</h1>
      </header>

      {page === 'search' ? (
        <main className="layout content-stack">
          <section className="card">
            <h2>Поиск</h2>
            <form onSubmit={handleSearch} className="form-grid">
              <div className="search-query-row">
                <label className="search-query-field">
                  Запрос *
                  <input value={query} onChange={(event) => setQuery(event.target.value)} required />
                </label>

                <button type="submit" disabled={isSearching}>
                  {isSearching ? 'Поиск...' : 'Найти'}
                </button>
              </div>

              <label>
                Фильтр по `scope`
                <input
                  value={filterScope}
                  onChange={(event) => setFilterScope(event.target.value)}
                  placeholder="Например: hr"
                />
              </label>

              <div className="slider-grid slider-grid-inline">
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
            </form>
            {searchError != null ? <p className="message error">{searchError}</p> : null}

            <div className="results">
              {results.length === 0 ? (
                <p className="empty">Результаты появятся здесь.</p>
              ) : (
                results.map((result) => (
                  <article key={result.chunk_id} className="result-card">
                    <div className="result-meta">
                      <span>документ: {result.title}</span>
                      <span>semantic: {floatFormat.format(result.semantic_score)}</span>
                      <span>temporal: {floatFormat.format(result.temporal_score)}</span>
                      <strong>score: {floatFormat.format(result.combined_score)}</strong>
                    </div>
                    <p>{highlightExactMatch(result.content, query)}</p>
                  </article>
                ))
              )}
            </div>
          </section>
        </main>
      ) : (
        <main className="layout content-stack">
          <section className="card">
            <h2>Загрузка и список документов</h2>

            <div className="documents-grid">
              <section className="documents-panel">
                <h3>Загрузка .txt документа</h3>
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
              </section>

              <section className="documents-panel">
                <h3>Загруженные документы</h3>
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
                        {recentDocumentLine(document)}
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </div>
          </section>
        </main>
      )}
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