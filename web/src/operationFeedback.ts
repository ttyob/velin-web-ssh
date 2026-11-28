export function operationLabel(key: string): string {
  if (key.startsWith('commit')) return '正在创建提交…';
  if (key.startsWith('merge:')) return '正在合并分支…';
  if (key.startsWith('switch')) return '正在切换分支…';
  return ({ fetch: '正在获取远程状态…', pull: '正在拉取最新代码…', push: '正在推送提交…', stage: '正在准备提交…', 'ai-commit': '正在生成提交说明…', 'merge-abort': '正在中止合并…', 'merge-continue': '正在完成合并…' } as Record<string, string>)[key] || '正在处理…';
}
export function operationHint(error: string): string {
  if (/conflict|冲突|unmerged|resolve.*index/i.test(error)) return '请处理冲突后继续；当前文件和提交说明已保留。';
  if (/non-fast-forward|fetch first|diverg|落后/i.test(error)) return '远程分支已有更新，请先获取远程状态并检查变更。';
  if (/authentication|permission denied|publickey|权限|认证|credential/i.test(error)) return '请检查主机凭据、仓库地址和访问权限，然后重试。';
  if (/overwritten|local changes|would be removed/i.test(error)) return '请先提交或备份本地修改，再执行此操作。';
  if (/timed? ?out|network|connect|连接|超时|502|503|504|resolve host/i.test(error)) return '请检查主机与网络连接；可以重新连接并读取当前状态。';
  return '请查看错误详情，刷新状态后再操作；当前内容已保留。';
}
