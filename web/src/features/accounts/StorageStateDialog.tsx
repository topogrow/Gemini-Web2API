import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Code2, AlertTriangle } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/input'
import { Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { accountsKey } from '@/features/accounts/queries'
import { validateAndFormatJSON } from '@/features/accounts/storageStateHelpers'
import { api } from '@/lib/api'

interface StorageStateDialogProps {
  accountId: string
  onClose: () => void
}

export function StorageStateDialog({ accountId, onClose }: StorageStateDialogProps) {
  const client = useQueryClient()
  const [jsonText, setJsonText] = useState('')

  // 1. 读取该账号现有的 storage-state.json 内容
  const stateQuery = useQuery({
    queryKey: ['storage-state', accountId],
    queryFn: () =>
      api<{ content: string }>('/api/accounts/' + encodeURIComponent(accountId) + '/storage-state'),
  })

  useEffect(() => {
    if (stateQuery.data?.content) {
      const res = validateAndFormatJSON(stateQuery.data.content)
      setJsonText(res.formatted || stateQuery.data.content)
    }
  }, [stateQuery.data?.content])

  // 2. 实时校验当前文本
  const validation = validateAndFormatJSON(jsonText)

  // 3. 写回更新并热重载账号
  const saveMutation = useMutation({
    mutationFn: (content: string) =>
      api('/api/accounts/' + encodeURIComponent(accountId) + '/storage-state', {
        method: 'PUT',
        json: { content },
      }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      toast({
        tone: 'success',
        title: `✅ 账号 [${accountId}] 的 Cookie JSON 已更新并重新载入生效！`,
      })
      onClose()
    },
    onError: (error) => {
      toast({ tone: 'error', title: '保存失败: ' + error.message })
    },
  })

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        title={`修改账号 [${accountId}] 的 Cookie JSON`}
        description="你可以在此处直接查看、粘贴更新 Playwright / storage-state 格式的完整 Cookie JSON。保存后系统将自动原子写盘并即刻热重载该账号。"
        closeLabel="取消"
        footer={
          <>
            <div className="mr-auto flex items-center gap-2 text-xs">
              {validation.valid ? (
                <>
                  <Badge tone="success">
                    <CheckCircle2 size={12} className="mr-1 inline" />
                    格式合法 ({validation.cookiesCount} 个 Cookie)
                  </Badge>
                  {validation.hasSidts ? (
                    <span className="text-success-text">已含核心 __Secure-1PSIDTS</span>
                  ) : (
                    <span className="text-warning-text">缺少 __Secure-1PSIDTS (可能失效)</span>
                  )}
                </>
              ) : (
                <Badge tone="error">
                  <AlertTriangle size={12} className="mr-1 inline" />
                  JSON 语法错误
                </Badge>
              )}
            </div>
            <Button
              variant="secondary"
              type="button"
              disabled={!validation.valid}
              onClick={() => {
                if (validation.valid) setJsonText(validation.formatted)
              }}
            >
              格式化排版
            </Button>
            <DialogClose asChild>
              <Button variant="secondary">取消</Button>
            </DialogClose>
            <Button
              disabled={!validation.valid || saveMutation.isPending}
              loading={saveMutation.isPending}
              onClick={() => {
                saveMutation.mutate(jsonText)
              }}
            >
              保存并热重载
            </Button>
          </>
        }
      >
        {stateQuery.isPending ? (
          <Loading label="正在读取 Cookie JSON..." />
        ) : (
          <div className="flex flex-col gap-3 py-1">
            <Field
              id="storage-json"
              label="storage-state.json 原始内容"
              description="支持完整 Playwright storageState 对象（含 cookies 与 origins 数组）"
            >
              <Textarea
                id="storage-json"
                className="h-[320px] font-mono text-xs leading-5 select-all"
                value={jsonText}
                placeholder="粘贴新的 storage-state.json 内容..."
                onChange={(e) => setJsonText(e.target.value)}
              />
            </Field>
            {validation.error && (
              <p className="rounded bg-error-soft px-3 py-2 text-xs text-error-text">
                错误提示: {validation.error}
              </p>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
