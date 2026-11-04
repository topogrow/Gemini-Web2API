import { describe, expect, it } from 'vitest'
import type { Account } from './types'
import { isAccountCaptchaChallenged } from './captchaHelpers'

describe('Captcha Detection and Action Helper', () => {
  it('当 last_error 包含“人机验证”时，应识别为需要人工解除', () => {
    const acc: Partial<Account> = {
      account_id: 'test-user',
      state: 'unavailable',
      last_error: 'gemini bootstrap 被 Google 人机验证页拦截（HTTP 302 重定向到 www.google.com/sorry/index）',
    }
    expect(isAccountCaptchaChallenged(acc)).toBe(true)
  })

  it('当 cooldown_reason 为 captcha 时，应识别为需要人工解除', () => {
    const acc: Partial<Account> = {
      account_id: 'test-user2',
      state: 'cooldown',
      cooldown_reason: 'captcha',
    }
    expect(isAccountCaptchaChallenged(acc)).toBe(true)
  })

  it('当正常就绪且无人机报错时，应返回 false', () => {
    const acc: Partial<Account> = {
      account_id: 'test-user3',
      state: 'ready',
      last_error: undefined,
    }
    expect(isAccountCaptchaChallenged(acc)).toBe(false)
  })
})
