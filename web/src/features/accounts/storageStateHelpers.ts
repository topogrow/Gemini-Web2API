export interface ValidationResult {
  valid: boolean
  formatted: string
  error?: string
  cookiesCount: number
  hasSidts: boolean
}

// normalizeCookie 将不同插件格式（如 expirationDate）规范化为标准属性
export function normalizeCookie(item: any): any {
  if (!item || typeof item !== 'object') return null
  const expires =
    item.expires !== undefined ? item.expires : item.expirationDate !== undefined ? item.expirationDate : -1

  return {
    name: String(item.name || ''),
    value: String(item.value || ''),
    domain: String(item.domain || ''),
    path: String(item.path || '/'),
    expires: typeof expires === 'number' ? expires : -1,
    httpOnly: Boolean(item.httpOnly),
    secure: Boolean(item.secure),
    sameSite: item.sameSite || undefined,
  }
}

export function parseStorageStateCookies(obj: any): { count: number; hasSidts: boolean; normalizedList: any[] } {
  if (!obj) {
    return { count: 0, hasSidts: false, normalizedList: [] }
  }

  let cookieArray: any[] = []
  if (Array.isArray(obj)) {
    // 1. 用户直接粘贴纯 Cookie 数组（如 Cookie-Editor / EditThisCookie）
    cookieArray = obj
  } else if (Array.isArray(obj.cookies)) {
    // 2. 标准 Playwright storageState 对象
    cookieArray = obj.cookies
  }

  const normalizedList = cookieArray.map(normalizeCookie).filter(Boolean)
  let hasSidts = false
  for (const c of normalizedList) {
    if (c.name === '__Secure-1PSIDTS') {
      hasSidts = true
      break
    }
  }

  return { count: normalizedList.length, hasSidts, normalizedList }
}

export function validateAndFormatJSON(raw: string, existingMetadata?: any): ValidationResult {
  if (!raw || !raw.trim()) {
    return { valid: false, formatted: '', error: '内容不能为空', cookiesCount: 0, hasSidts: false }
  }

  try {
    const parsed = JSON.parse(raw)
    const { count, hasSidts, normalizedList } = parseStorageStateCookies(parsed)

    let finalObject: any
    if (Array.isArray(parsed)) {
      // 自动包裹纯数组为标准的 StorageState 结构
      finalObject = {
        cookies: normalizedList,
        origins: [],
        ...(existingMetadata ? { geminiWeb2api: existingMetadata } : {}),
      }
    } else {
      finalObject = {
        ...parsed,
        cookies: normalizedList,
        origins: parsed.origins || [],
      }
    }

    const formatted = JSON.stringify(finalObject, null, 2)
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
      error: err.message || 'JSON 语法解析错误',
      cookiesCount: 0,
      hasSidts: false,
    }
  }
}
