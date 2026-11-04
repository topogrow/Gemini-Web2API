import type { Account } from './types'

export function isAccountCaptchaChallenged(account: Partial<Account>): boolean {
  if (!account) return false
  if (account.cooldown_reason === 'captcha') return true
  const err = account.last_error || ''
  return err.includes('人机验证') || err.includes('sorry/index') || err.includes('captcha')
}
