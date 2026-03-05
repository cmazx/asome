import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  defaultSearchModifiers,
  fetchRecentDocuments,
  maxUploadSizeBytes,
  toSearchPayload,
  validateUploadFile,
} from './api.ts'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('validateUploadFile', () => {
  it('ошибка, когда файл не выбран', () => {
    expect(validateUploadFile(null)).toBe('Выберите .txt файл')
  })

  it('ошибка, когда файл не txt', () => {
    const file = { name: 'notes.pdf', size: 10 } as File
    expect(validateUploadFile(file)).toBe('Можно загрузить только .txt файл')
  })

  it('ошибка, когда файл больше 100 МБ', () => {
    const file = { name: 'big.txt', size: maxUploadSizeBytes + 1 } as File
    expect(validateUploadFile(file)).toBe('Размер файла не должен превышать 100 МБ')
  })

  it('валидный txt файл проходит проверку', () => {
    const file = { name: 'ok.txt', size: maxUploadSizeBytes } as File
    expect(validateUploadFile(file)).toBeNull()
  })
})

describe('toSearchPayload', () => {
  it('формирует payload с дефолтами и триммингом query', () => {
    const payload = toSearchPayload({
      query: '  hello world  ',
      filterScope: '',
      timeDecayDays: defaultSearchModifiers.timeDecayDays,
      semanticWeight: defaultSearchModifiers.semanticWeight,
      fulltextWeight: defaultSearchModifiers.fulltextWeight,
      tempWeight: defaultSearchModifiers.tempWeight,
      resultLimit: defaultSearchModifiers.resultLimit,
    })

    expect(payload).toEqual({
      query: 'hello world',
      time_decay_days: 365,
      semantic_weight: 0.7,
      fulltext_weight: 0.2,
      temp_weight: 0.3,
      result_limit: 10,
    })
  })

  it('добавляет filter_scope только когда он не пустой', () => {
    const payload = toSearchPayload({
      query: 'query',
      filterScope: '  hr ',
      timeDecayDays: 100,
      semanticWeight: 0.6,
      fulltextWeight: 0.2,
      tempWeight: 0.2,
      resultLimit: 7,
    })

    expect(payload).toEqual({
      query: 'query',
      filter_scope: 'hr',
      time_decay_days: 100,
      semantic_weight: 0.6,
      fulltext_weight: 0.2,
      temp_weight: 0.2,
      result_limit: 7,
    })
  })
})

describe('fetchRecentDocuments', () => {
  it('возвращает список последних документов', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ documents: [{ title: 'doc.txt', processing_time: 9 }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const documents = await fetchRecentDocuments()

    expect(documents).toEqual([{ title: 'doc.txt', processing_time: 9 }])
    expect(fetchMock).toHaveBeenCalledWith('/api/documents/recent', undefined)
  })

  it('повторяет запрос без /api, если первый ответ 404', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: 'not found' }), {
          status: 404,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ documents: [{ title: 'doc2.txt', processing_error: 'oops' }] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    vi.stubGlobal('fetch', fetchMock)

    const documents = await fetchRecentDocuments()

    expect(documents).toEqual([{ title: 'doc2.txt', processing_error: 'oops' }])
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/documents/recent', undefined)
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/documents/recent', undefined)
  })

  it('выбрасывает ошибку, когда запрос завершился неуспешно', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: 'boom' }), {
          status: 500,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    )

    await expect(fetchRecentDocuments()).rejects.toThrow('boom')
  })
})