import { describe, expect, it } from 'vitest'
import { parseStorageStateCookies, validateAndFormatJSON } from './storageStateHelpers'

describe('StorageState JSON Helper and Validation', () => {
  it('应当正确格式化合法 JSON 并统计 Cookie 信息', () => {
    const raw = '{"cookies":[{"name":"SID","value":"123"},{"name":"__Secure-1PSIDTS","value":"abc"}]}'
    const result = validateAndFormatJSON(raw)
    expect(result.valid).toBe(true)
    expect(result.formatted).toContain('\n')
    expect(result.cookiesCount).toBe(2)
    expect(result.hasSidts).toBe(true)
  })

  it('应当对非法 JSON 返回错误提示', () => {
    const raw = '{ invalid json'
    const result = validateAndFormatJSON(raw)
    expect(result.valid).toBe(false)
    expect(result.error).toBeDefined()
    expect(result.cookiesCount).toBe(0)
  })

  it('应当兼容普通 Cookie 字符串数组提取', () => {
    const obj = {
      cookies: [
        { name: 'SID', value: '111' },
        { name: '__Secure-1PSID', value: '222' }
      ]
    }
    const res = parseStorageStateCookies(obj)
    expect(res.count).toBe(2)
    expect(res.hasSidts).toBe(false)
  })
})
