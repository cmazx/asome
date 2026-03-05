import { describe, expect, it } from 'vitest'
import {
  defaultSearchModifiers,
  maxUploadSizeBytes,
  toSearchPayload,
  validateUploadFile,
} from './api.ts'

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