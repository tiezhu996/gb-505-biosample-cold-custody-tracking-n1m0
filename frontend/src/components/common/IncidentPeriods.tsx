import { AlertOutlined } from '@ant-design/icons'
import { Alert, Empty, Tag, Timeline, Typography } from 'antd'
import type { Specimen, TemperatureIncidentItem } from '../../types/domain'
import { formatDateTime } from '../../utils/format'
import { StatusBadge } from './StatusBadge'

// OpenIncidentWarning is shown wherever a specimen is handled while a
// temperature exception order is still open.
export function OpenIncidentWarning({ specimen }: { specimen: Specimen }) {
  const open = (specimen.incidentItems || []).filter((item) => item.incident?.state === 'open')
  if (!open.length) return null
  return (
    <Alert
      type="error" showIcon icon={<AlertOutlined />} style={{ marginBottom: 12 }}
      message="样本处于温度异常处置期间"
      description={open.map((item) => (
        <div key={item.id}>
          处置单 <Typography.Text strong>{item.incident?.incidentNo}</Typography.Text>
          {' '}（{item.incident?.container?.code}）未结案，交接与协议放行已暂缓；
          异常起始 {formatDateTime(item.incident?.startedAt)}，起始温度 {item.incident?.startTempC}°C。
        </div>
      ))}
    />
  )
}

// IncidentPeriods lists every temperature exception period a specimen lived
// through. The history stays visible after the order is closed.
export function IncidentPeriods({ items }: { items?: TemperatureIncidentItem[] }) {
  if (!items?.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无温度异常时段记录" />
  return (
    <Timeline items={items.map((item) => {
      const incident = item.incident
      const relocated = Boolean(item.relocatedAt)
      return {
        color: incident?.state === 'open' ? 'red' : relocated ? 'blue' : 'green',
        dot: incident?.state === 'open' ? <AlertOutlined /> : undefined,
        children: (
          <div className="timeline-item">
            <div>
              <Typography.Text strong>{incident?.incidentNo || `处置单 #${item.incidentId}`}</Typography.Text>{' '}
              {incident && <StatusBadge value={incident.state} />}{' '}
              {relocated && <Tag color="blue">已转柜</Tag>}
            </div>
            <Typography.Text>
              {incident?.container ? `${incident.container.code} · ${incident.container.name}` : '原容器'}
              ，处置人 {incident?.handlerName || '-'}
            </Typography.Text>
            <small>
              异常时段 {formatDateTime(incident?.startedAt)} → {formatDateTime(incident?.resolvedAt)}
              {' '}· {incident?.startTempC}°C → {incident?.endTempC != null ? `${incident.endTempC}°C` : '待恢复'}
            </small>
            <small>
              原格位 {item.snapshotPosition || '-'}
              {relocated ? ` → ${item.targetContainer?.code || ''} / ${item.targetPosition}（${item.relocatedByName}，${formatDateTime(item.relocatedAt)}）` : '（留柜观察）'}
            </small>
            {incident?.alarmReason && <Typography.Paragraph type="secondary">报警原因：{incident.alarmReason}</Typography.Paragraph>}
            {incident?.conclusion && <Typography.Paragraph type="secondary">恢复结论：{incident.conclusion}</Typography.Paragraph>}
          </div>
        ),
      }
    })} />
  )
}
