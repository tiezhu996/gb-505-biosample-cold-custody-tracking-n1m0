import { AlertOutlined } from '@ant-design/icons'
import { Empty, Tag, Timeline, Tooltip, Typography } from 'antd'
import type { TemperatureExceptionItem } from '../../types/domain'
import { formatDateTime } from '../../utils/format'
import { StatusBadge } from './StatusBadge'

export interface ExceptionEntry {
  id: number
  exceptionNo: string
  state: 'open' | 'closed'
  action: 'onsite' | 'relocation'
  startTemperatureC: number
  endTemperatureC?: number
  outcome?: string
  alarmReason: string
  handlerName: string
  startedAt: string
  closedAt?: string
  conclusionNotes?: string
  containerCode?: string
  moved?: boolean
  targetLabel?: string
  itemNotes?: string
}

export function toExceptionEntries(items?: TemperatureExceptionItem[]): ExceptionEntry[] {
  if (!items) return []
  return items
    .filter((item) => item.exception)
    .map((item) => {
      const exception = item.exception!
      return {
        id: exception.id,
        exceptionNo: exception.exceptionNo,
        state: exception.state,
        action: exception.action,
        startTemperatureC: exception.startTemperatureC,
        endTemperatureC: exception.endTemperatureC,
        outcome: exception.outcome,
        alarmReason: exception.alarmReason,
        handlerName: exception.handlerName,
        startedAt: exception.startedAt,
        closedAt: exception.closedAt,
        conclusionNotes: exception.conclusionNotes,
        containerCode: exception.container?.code,
        moved: item.moved,
        targetLabel: item.targetContainer ? `${item.targetContainer.code} / ${item.targetPosition || '-'}` : item.targetPosition,
        itemNotes: item.notes,
      }
    })
    .sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime())
}

export function OpenExceptionTag({ entries }: { entries: ExceptionEntry[] }) {
  const open = entries.find((entry) => entry.state === 'open')
  if (!open) return null
  return (
    <Tooltip title={`${open.exceptionNo} · ${open.alarmReason}`}>
      <Tag icon={<AlertOutlined />} color="error">温度异常暂缓中</Tag>
    </Tooltip>
  )
}

// SpecimenHoldTag 在样本被未结案处置单覆盖，或其所在容器处于温度告警时，
// 给出统一的暂缓交接/放行标记。
export function SpecimenHoldTag({ specimen }: { specimen?: {
  temperatureItems?: TemperatureExceptionItem[]
  storageContainer?: { code?: string; status?: string }
} | null }) {
  if (!specimen) return null
  const entries = toExceptionEntries(specimen.temperatureItems)
  const open = entries.find((entry) => entry.state === 'open')
  if (open) {
    return (
      <Tooltip title={`${open.exceptionNo} · ${open.alarmReason}`}>
        <Tag icon={<AlertOutlined />} color="error">温度异常暂缓中</Tag>
      </Tooltip>
    )
  }
  if (specimen.storageContainer?.status === 'alarm') {
    return (
      <Tooltip title={`容器 ${specimen.storageContainer.code || ''} 存在未结案温度异常处置单`}>
        <Tag icon={<AlertOutlined />} color="error">温度异常暂缓中</Tag>
      </Tooltip>
    )
  }
  return null
}

// specimenBlocked 判断样本是否处于温度异常暂缓：存在未结案处置明细，
// 或其当前所在容器已处于温度告警状态（现场观察只登记重点样本的情形）。
export function specimenBlocked(specimen?: {
  temperatureItems?: TemperatureExceptionItem[]
  storageContainer?: { status?: string }
} | null): boolean {
  if (!specimen) return false
  if (toExceptionEntries(specimen.temperatureItems).some((entry) => entry.state === 'open')) return true
  return specimen.storageContainer?.status === 'alarm'
}

export function ExceptionTimeline({ entries }: { entries: ExceptionEntry[] }) {
  if (!entries.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无温度异常处置记录" />
  return (
    <Timeline items={entries.map((entry) => ({
      color: entry.state === 'open' ? 'red' : 'gray',
      dot: <AlertOutlined />,
      children: (
        <div className="timeline-item">
          <div>
            <Typography.Text strong>{entry.exceptionNo}</Typography.Text>{' '}
            <StatusBadge value={entry.state} /> <StatusBadge value={entry.action} />
            {entry.outcome && <StatusBadge value={entry.outcome} />}
          </div>
          <Typography.Text>
            {entry.containerCode ? `${entry.containerCode} · ` : ''}
            起始 {entry.startTemperatureC}°C{entry.endTemperatureC != null ? ` → 结束 ${entry.endTemperatureC}°C` : ''}
          </Typography.Text>
          <small>
            {formatDateTime(entry.startedAt)}{entry.closedAt ? ` 至 ${formatDateTime(entry.closedAt)}` : ' 起'} · 处置人 {entry.handlerName}
          </small>
          <Typography.Paragraph type="secondary">{entry.alarmReason}</Typography.Paragraph>
          {entry.moved && <small>转柜目标：{entry.targetLabel || '-'}</small>}
          {entry.itemNotes && <Typography.Paragraph type="secondary">样本备注：{entry.itemNotes}</Typography.Paragraph>}
          {entry.conclusionNotes && <Typography.Paragraph type="secondary">结案结论：{entry.conclusionNotes}</Typography.Paragraph>}
        </div>
      ),
    }))} />
  )
}
