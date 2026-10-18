import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, FileCode, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, Section } from '@/components/ui/page'
import { toast } from '@/components/ui/toast'
import { accountsKey } from '@/features/accounts/queries'
import { StorageStateDialog } from '@/features/accounts/StorageStateDialog'
import type { Account } from '@/features/accounts/types'
import { api } from '@/lib/api'

interface AccountProfilePanelProps {
  account: Account
}

export function AccountProfilePanel({ account }: AccountProfilePanelProps) {
  const client = useQueryClient()
  const id = account.account_id
  const [editJsonOpen, setEditJsonOpen] = useState(false)

  // 1. 唤起专属 Chrome 窗口进行登录/重登
  const launchBrowser = useMutation({
    mutationFn: () =>
      api('/api/accounts/' + encodeURIComponent(id) + '/launch-browser', { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      toast({
        tone: 'success',
        title: '独立沙盒登录完成，凭据已刷新并自动加入后台保活池！',
      })
    },
    onError: (error) => {
      toast({ tone: 'error', title: '启动独立环境登录失败: ' + error.message })
    },
  })

  // 2. 触发后台无头静默续签测试
  const headlessRefresh = useMutation({
    mutationFn: () =>
      api<{ success: boolean; cookies_count: number }>(
        '/api/accounts/' + encodeURIComponent(id) + '/headless-refresh',
        { method: 'POST' },
      ),
    onSuccess: (data) => {
      void client.invalidateQueries({ queryKey: accountsKey })
      toast({
        tone: 'success',
        title: `静默续签成功！已提取并更新 ${data.cookies_count} 个最新 Cookie`,
      })
    },
    onError: (error) => {
      toast({ tone: 'error', title: '静默续签失败: ' + error.message })
    },
  })

  // 3. 重置并清空本地沙盒目录
  const resetProfile = useMutation({
    mutationFn: () =>
      api('/api/accounts/' + encodeURIComponent(id) + '/reset-profile', { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      toast({ tone: 'neutral', title: '本地独立沙盒目录已清空重置' })
    },
    onError: (error) => {
      toast({ tone: 'error', title: '重置沙盒失败: ' + error.message })
    },
  })

  return (
    <Card>
      <CardHeader
        title="本地独立沙盒环境 (Chrome Profile)"
        actions={
          account.has_profile ? (
            <Badge tone="success">独立环境已建立</Badge>
          ) : (
            <Badge tone="warning">未绑定独立环境</Badge>
          )
        }
      />
      <div className="flex flex-col gap-4 px-5 py-4 text-sm">
        <div className="flex flex-col gap-1 text-xs text-foreground-muted">
          <div>
            <span className="font-semibold text-foreground">物理隔离路径: </span>
            <code className="rounded bg-surface px-1.5 py-0.5 font-mono select-all">
              {account.profile_path || '尚未建立专属沙盒'}
            </code>
          </div>
          <div className="mt-1">
            <span className="font-semibold text-foreground">自动保活机制: </span>
            {account.has_profile ? (
              <span className="text-success-text">
                已启用后台无头 Chrome CDP 静默滚动续签，永久防止 Cookie 失效
              </span>
            ) : (
              <span className="text-warning-text">
                当前仍为传统静态 Cookie，缺少本地沙盒，易在数小时后失效；建议立即点击下方按钮绑定。
              </span>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3 pt-2">
          <Button
            size="sm"
            loading={launchBrowser.isPending}
            onClick={() => {
              toast({
                tone: 'neutral',
                title: '正在拉起独立 Chrome 窗口，请在弹出的浏览器中登录 Google 账号...',
              })
              launchBrowser.mutate()
            }}
          >
            <ExternalLink size={16} aria-hidden />
            {account.has_profile ? '打开独立环境重登' : '创建沙盒并登录'}
          </Button>

          {account.has_profile && (
            <>
              <Button
                variant="secondary"
                size="sm"
                loading={headlessRefresh.isPending}
                onClick={() => {
                  headlessRefresh.mutate()
                }}
              >
                <RefreshCw size={16} aria-hidden />
                测试后台静默续签
              </Button>

              <Button
                variant="destructive"
                size="sm"
                loading={resetProfile.isPending}
                onClick={() => {
                  if (confirm('确定要清空该账号的本地 Chrome 沙盒目录吗？')) {
                    resetProfile.mutate()
                  }
                }}
              >
                <Trash2 size={16} aria-hidden />
                重置沙盒
              </Button>
            </>
          )}

          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setEditJsonOpen(true)
            }}
          >
            <FileCode size={16} aria-hidden />
            修改 Cookie JSON
          </Button>
        </div>
      </div>

      {editJsonOpen && (
        <StorageStateDialog accountId={id} onClose={() => setEditJsonOpen(false)} />
      )}
    </Card>
  )
}
