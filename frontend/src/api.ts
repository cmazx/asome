export const maxUploadSizeBytes = 100 << 20

export const defaultSearchModifiers = {
  timeDecayDays: 365,
  semanticWeight: 0.7,
  fulltextWeight: 0.2,
  tempWeight: 0.3,
  resultLimit: 10,
} as const

const configuredApiBase = import.meta.env.VITE_API_BASE?.trim()
const recentDocumentsLimit = 20

function normalizeApiBase(base: string): string {
  if (base === '/') {
    return ''
  }

  return base.replace(/\/+$/, '')
}

const apiBases = configuredApiBase != null && configuredApiBase !== ''
  ? [normalizeApiBase(configuredApiBase)]
  : ['/api', '']

function buildApiURL(path: string, base: string): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${base}${normalizedPath}`
}

type ApiErrorPayload = {
  error?: string
}

export type UploadDocumentInput = {
  file: File
  scope: string
  source?: string
  docType?: string
  version?: number
}

export type UploadResponse = {
  id: string
  temporal_path: string
}

export type SearchInput = {
  query: string
  timeDecayDays: number
  semanticWeight: number
  fulltextWeight: number
  tempWeight: number
  resultLimit: number
  filterScope: string
}

export type SearchResult = {
  chunk_id: string
  document_id: string
  content: string
  title: string
  semantic_score: number
  temporal_score: number
  combined_score: number
}

export type RecentDocument = {
  title: string
  processing_time?: number
  processing_error?: string
}

export function validateUploadFile(file: File | null): string | null {
  if (file == null) {
    return 'Выберите .txt файл'
  }
  if (!file.name.toLowerCase().endsWith('.txt')) {
    return 'Можно загрузить только .txt файл'
  }
  if (file.size > maxUploadSizeBytes) {
    return 'Размер файла не должен превышать 100 МБ'
  }
  return null
}

export function toSearchPayload(input: SearchInput) {
  const payload: Record<string, string | number> = {
    query: input.query.trim(),
    time_decay_days: input.timeDecayDays,
    semantic_weight: input.semanticWeight,
    fulltext_weight: input.fulltextWeight,
    temp_weight: input.tempWeight,
    result_limit: input.resultLimit,
  }

  const filterScope = input.filterScope.trim()
  if (filterScope !== '') {
    payload.filter_scope = filterScope
  }

  return payload
}

async function parseError(response: Response, fallback: string): Promise<string> {
  try {
    const payload = (await response.json()) as ApiErrorPayload
    return payload.error || fallback
  } catch {
    return fallback
  }
}

async function fetchAPI(path: string, init?: RequestInit): Promise<Response> {
  for (const [index, base] of apiBases.entries()) {
    const response = await fetch(buildApiURL(path, base), init)
    if (response.status !== 404 || index === apiBases.length - 1) {
      return response
    }
  }

  throw new Error('Не удалось выполнить запрос')
}

export async function uploadDocument(input: UploadDocumentInput): Promise<UploadResponse> {
  const body = new FormData()
  body.append('file', input.file)
  body.append('scope', input.scope)

  if (input.source?.trim()) {
    body.append('source', input.source.trim())
  }
  if (input.docType?.trim()) {
    body.append('doc_type', input.docType.trim())
  }
  if (input.version != null) {
    body.append('version', String(input.version))
  }

  const response = await fetchAPI('/documents', {
    method: 'POST',
    body,
  })

  if (!response.ok) {
    throw new Error(await parseError(response, 'Не удалось загрузить документ'))
  }

  return (await response.json()) as UploadResponse
}

export async function search(input: SearchInput): Promise<SearchResult[]> {
  const response = await fetchAPI('/search', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(toSearchPayload(input)),
  })

  if (!response.ok) {
    throw new Error(await parseError(response, 'Не удалось выполнить поиск'))
  }

  const payload = (await response.json()) as { results: SearchResult[] }
  return payload.results
}

export async function fetchRecentDocuments(): Promise<RecentDocument[]> {
  const response = await fetchAPI(`/documents/recent?limit=${recentDocumentsLimit}`)
  if (!response.ok) {
    throw new Error(await parseError(response, 'Не удалось получить последние документы'))
  }

  const payload = (await response.json()) as { documents: RecentDocument[] }
  return payload.documents
}