export interface ValidationResult {
  valid: boolean
  formatted: string
  error?: string
  cookiesCount: number
  hasSidts: boolean
}

export function parseStorageStateCookies(obj: any): { count: number; hasSidts: boolean } {
  if (!obj || typeof obj !== 'object') {
    return { count: 0, hasSidts: false }
  }
  const cookies = Array.isArray(obj.cookies) ? obj.cookies : []
  let hasSidts = false
  for (const c of cookies) {
    if (c && c.name === '__Secure-1PSIDTS') {
      hasSidts = true
      break
    }
  }
  return { count: cookies.length, hasSidts }
}

export function validateAndFormatJSON(raw: string): ValidationResult {
  try {
    const parsed = JSON.parse(raw)
    const formatted = JSON.stringify(parsed, null, 2)
    const { count, hasSidts } = parseStorageStateCookies(parsed)
    return {
      valid: true,
      formatted,
      cookiesCount: count,
      hasSidts,
    }
  } catch (err: any) {
    return {
      valid: false,
      formatted: raw,
      error: err.message || 'JSON 格式解析错误',
      cookiesCount: 0,
      hasSidts: false,
    }
  }
}
