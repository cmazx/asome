// @vitest-environment jsdom

import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRoot, Root } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { App, highlightExactMatch } from './App'
import { fetchRecentDocuments, search, uploadDocument } from './api'

vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>()
  return {
    ...actual,
    fetchRecentDocuments: vi.fn(),
    search: vi.fn(),
    uploadDocument: vi.fn(),
  }
})

const fetchRecentDocumentsMock = vi.mocked(fetchRecentDocuments)
const searchMock = vi.mocked(search)
const uploadDocumentMock = vi.mocked(uploadDocument)

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root | null = null

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  window.location.hash = searchPageHash
  fetchRecentDocumentsMock.mockResolvedValue([])
  searchMock.mockResolvedValue([])
  uploadDocumentMock.mockResolvedValue({
    id: 'mock-id',
    temporal_path: '/tmp/mock-id',
  })
})

afterEach(() => {
  if (root != null) {
    act(() => {
      root?.unmount()
    })
  }
  vi.useRealTimers()
  container.remove()
  vi.clearAllMocks()
  window.location.hash = ''
})

const searchPageHash = '#/'
const documentsPageHash = '#/documents'

function renderApp() {
  root = createRoot(container)
  act(() => {
    root?.render(<App />)
  })
}

describe('App navigation', () => {
  it('на главной показывает только секцию поиска', () => {
    renderApp()

    const queryRow = container.querySelector('.search-query-row')
    const inlineSliders = container.querySelectorAll('.slider-grid-inline .slider')

    expect(container.textContent).toContain('Поиск')
    expect(container.textContent).toContain('Запрос *')
    expect(queryRow?.querySelector('button[type="submit"]')?.textContent).toContain('Найти')
    expect(inlineSliders).toHaveLength(5)
    expect(container.textContent).not.toContain('Сумма весов')
    expect(container.textContent).not.toContain('Загрузка и список документов')
    expect(fetchRecentDocumentsMock).not.toHaveBeenCalled()
  })

  it('по навигации открывает страницу документов и форматирует обработанные документы', async () => {
    fetchRecentDocumentsMock.mockResolvedValue([
      { title: 'alpha.txt', processing_time: 9 },
      { title: 'beta.txt', processing_error: 'ошибка обработки' },
    ])

    renderApp()

    act(() => {
      window.location.hash = documentsPageHash
      window.dispatchEvent(new Event('hashchange'))
    })

    await act(async () => {
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Загрузка и список документов')
    expect(container.textContent).toContain('Загрузка .txt документа')
    expect(container.textContent).toContain('Загруженные документы')
    expect(container.textContent).toContain('alpha.txt обработан за 9 секунд')
    expect(container.textContent).toContain('beta.txt — ошибка: ошибка обработки')
    expect(container.textContent).not.toContain('Запрос *')
    expect(fetchRecentDocumentsMock).toHaveBeenCalledTimes(1)
  })

  it('на странице документов обновляет список каждые 3 секунды', async () => {
    vi.useFakeTimers()

    fetchRecentDocumentsMock.mockResolvedValue([])
    renderApp()

    act(() => {
      window.location.hash = documentsPageHash
      window.dispatchEvent(new Event('hashchange'))
    })

    await act(async () => {
      await Promise.resolve()
    })

    expect(fetchRecentDocumentsMock).toHaveBeenCalledTimes(1)

    await act(async () => {
      vi.advanceTimersByTime(3000)
      await Promise.resolve()
    })

    expect(fetchRecentDocumentsMock).toHaveBeenCalledTimes(2)
  })

  it('подсвечивает точные совпадения поискового слова в результатах', () => {
    const markup = renderToStaticMarkup(
      <p>{highlightExactMatch('Кот спит. котик играет. кот бежит.', 'кот')}</p>,
    )

    const highlightsCount = markup.match(/search-highlight/g)?.length ?? 0
    expect(highlightsCount).toBe(2)
    expect(markup).toContain('<mark class="search-highlight">Кот</mark>')
    expect(markup).toContain('<mark class="search-highlight">кот</mark>')
    expect(markup).toContain('котик')
  })
})