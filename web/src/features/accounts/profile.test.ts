import { describe, expect, it } from 'vitest'
import type { AccountStatus } from './types'

// 辅助逻辑：根据 AccountStatus 判断展示的状态徽标与建议动作
export function getProfileDisplayState(account: Partial<AccountStatus>) {
  if (account.has_profile) {
    if (account.state === 'auth_required' || account.state === 'unavailable') {
      return { tone: 'error' as const, label: '沙盒已就绪·会话失效需重登', action: 'relogin' }
    }
    return { tone: 'success' as const, label: '独立沙盒已就绪', action: 'manage' }
  }
  return { tone: 'warning' as const, label: '未绑定独立沙盒', action: 'create' }
}

describe('Account Profile Display State Logic', () => {
  it('应当为已绑定沙盒且状态正常的账号返回 success 徽标', () => {
    const status: Partial<AccountStatus> = {
      account_id: 'user1-gmail-com',
      state: 'ready',
      has_profile: true,
      profile_path: '/path/to/profile'
    }
    const res = getProfileDisplayState(status)
    expect(res.tone).toBe('success')
    expect(res.label).toBe('独立沙盒已就绪')
    expect(res.action).toBe('manage')
  })

  it('应当为已绑定沙盒但会话失效的账号返回 error 徽标并提示重登', () => {
    const status: Partial<AccountStatus> = {
      account_id: 'user2-gmail-com',
      state: 'auth_required',
      has_profile: true,
    }
    const res = getProfileDisplayState(status)
    expect(res.tone).toBe('error')
    expect(res.label).toContain('会话失效需重登')
    expect(res.action).toBe('relogin')
  })

  it('应当为未建立沙盒的传统静态 Cookie 账号返回 warning 徽标并提示创建', () => {
    const status: Partial<AccountStatus> = {
      account_id: 'user3-gmail-com',
      state: 'ready',
      has_profile: false,
    }
    const res = getProfileDisplayState(status)
    expect(res.tone).toBe('warning')
    expect(res.label).toBe('未绑定独立沙盒')
    expect(res.action).toBe('create')
  })
})
