import {
  CheckCircleOutlined, EnvironmentOutlined, PlusOutlined, SearchOutlined, SwapOutlined,
} from '@ant-design/icons'
import {
  Alert, Button, Col, Descriptions, Drawer, Form, Input, InputNumber, Modal, Row, Select,
  Space, Statistic, Table, Tag, Typography, message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { incidentAPI, storageAPI } from '../api'
import { EntityTable } from '../components/common/EntityTable'
import { StatusBadge } from '../components/common/StatusBadge'
import { useAuth } from '../hooks/useAuth'
import { usePagination } from '../hooks/usePagination'
import { useIncidentStore } from '../stores/incidentStore'
import type { IncidentState, StorageContainer, TemperatureIncident } from '../types/domain'
import { formatDateTime, temperatureLabels } from '../utils/format'

const zoneRank: Record<string, number> = { liquid_nitrogen: 3, minus80: 2, minus20: 1 }

interface RelocationDraft {
  specimenId: number
  targetContainerId?: number
  targetPosition?: string
}

export function IncidentsPage() {
  const { data, loading, load } = useIncidentStore()
  const pagination = usePagination()
  const { can } = useAuth()
  const [search, setSearch] = useState('')
  const [state, setState] = useState<IncidentState>()
  const [containers, setContainers] = useState<StorageContainer[]>([])
  const [createOpen, setCreateOpen] = useState(false)
  const [detail, setDetail] = useState<TemperatureIncident | null>(null)
  const [resolveTarget, setResolveTarget] = useState<TemperatureIncident | null>(null)
  const [drafts, setDrafts] = useState<Record<number, RelocationDraft>>({})
  const [saving, setSaving] = useState(false)
  const [createForm] = Form.useForm()
  const [resolveForm] = Form.useForm()
  const [searchParams, setSearchParams] = useSearchParams()
  const presetContainer = Number(searchParams.get('containerId')) || undefined
  const refresh = () => load({ page: pagination.page, pageSize: pagination.pageSize, search, state, containerId: presetContainer })
  useEffect(() => { void refresh() }, [pagination.page, pagination.pageSize, state, presetContainer])
  useEffect(() => { void storageAPI.list({ page: 1, pageSize: 100 }).then((result) => setContainers(result.items)) }, [])
  useEffect(() => {
    const preset = searchParams.get('containerId')
    const register = searchParams.get('alarm') === '1'
    if (preset && register && containers.length) {
      createForm.setFieldsValue({ containerId: Number(preset) })
      setCreateOpen(true)
      setSearchParams({}, { replace: true })
    }
  }, [searchParams, containers, createForm, setSearchParams])

  const openDetail = async (row: TemperatureIncident) => {
    const fresh = await incidentAPI.get(row.id)
    setDetail(fresh)
    const initial: Record<number, RelocationDraft> = {}
    fresh.items?.filter((item) => !item.relocatedAt).forEach((item) => { initial[item.specimenId] = { specimenId: item.specimenId } })
    setDrafts(initial)
  }

  const create = async () => {
    const values = await createForm.validateFields()
    setSaving(true)
    try {
      const created = await incidentAPI.create({
        containerId: values.containerId,
        startTempC: values.startTempC,
        alarmReason: values.alarmReason,
        startedAt: values.startedAt ? values.startedAt.toISOString() : undefined,
      })
      message.success(`温度异常处置单 ${created.incidentNo} 已登记，容器下样本交接与放行已暂缓`)
      setCreateOpen(false)
      createForm.resetFields()
      await Promise.all([load({ page: 1, pageSize: pagination.pageSize, search }), storageAPI.list({ page: 1, pageSize: 100 }).then((result) => setContainers(result.items))])
    } finally { setSaving(false) }
  }

  const submitRelocation = async () => {
    if (!detail) return
    const items = Object.values(drafts)
      .filter((draft) => draft.targetContainerId && draft.targetPosition?.trim())
    if (!items.length) {
      message.warning('请先为每支需要转柜的样本逐支选择目标容器和格位')
      return
    }
    const incomplete = items.some((item) => !item.targetContainerId || !item.targetPosition?.trim())
    if (incomplete) {
      message.warning('存在只填了容器或只填了格位的记录，请补全后再提交')
      return
    }
    setSaving(true)
    try {
      await incidentAPI.relocate(detail.id, items.map((item) => ({
        specimenId: item.specimenId,
        targetContainerId: item.targetContainerId as number,
        targetPosition: (item.targetPosition as string).trim(),
      })))
      message.success('转柜整单校验通过，全部目标格位已生效')
      await openDetail(detail)
      await refresh()
    } finally { setSaving(false) }
  }

  const resolve = async () => {
    if (!resolveTarget) return
    const values = await resolveForm.validateFields()
    setSaving(true)
    try {
      await incidentAPI.resolve(resolveTarget.id, {
        endTempC: values.endTempC,
        conclusion: values.conclusion,
        resolvedAt: values.resolvedAt ? values.resolvedAt.toISOString() : undefined,
      })
      message.success('处置单已结案，容器恢复可用，样本交接与放行解除暂缓')
      setResolveTarget(null)
      resolveForm.resetFields()
      if (detail?.id === resolveTarget.id) setDetail(null)
      await Promise.all([refresh(), storageAPI.list({ page: 1, pageSize: 100 }).then((result) => setContainers(result.items))])
    } finally { setSaving(false) }
  }

  const pendingItems = detail?.items?.filter((item) => !item.relocatedAt) || []
  const eligibleTargets = useMemo(() => {
    if (!detail?.container) return []
    const sourceRank = zoneRank[detail.container.temperatureZone] || 0
    return containers.filter((item) => item.id !== detail.containerId && item.active && item.status === 'available'
      && (zoneRank[item.temperatureZone] || 0) >= sourceRank && item.occupied < item.capacity)
  }, [containers, detail])

  const columns: ColumnsType<TemperatureIncident> = [
    { title: '处置单号', dataIndex: 'incidentNo', fixed: 'left' },
    { title: '容器', render: (_, row) => <div><strong>{row.container?.code || row.containerId}</strong><small className="cell-subtitle">{row.container?.name}</small></div> },
    { title: '状态', dataIndex: 'state', render: (value) => <StatusBadge value={value} dot /> },
    { title: '起始→结束温度', render: (_, row) => `${row.startTempC}°C → ${row.endTempC != null ? `${row.endTempC}°C` : '待恢复'}` },
    { title: '处置人', dataIndex: 'handlerName' },
    { title: '异常时段', render: (_, row) => <div>{formatDateTime(row.startedAt)}<small className="cell-subtitle">→ {formatDateTime(row.resolvedAt)}</small></div> },
    { title: '在控样本', render: (_, row) => row.items?.length ?? '-' },
    { title: '操作', fixed: 'right', render: (_, row) => <Button size="small" icon={<EnvironmentOutlined />} onClick={() => void openDetail(row)}>处置记录</Button> },
  ]

  type RelocationRow = NonNullable<TemperatureIncident['items']>[number]
  const relocationColumns: ColumnsType<RelocationRow> = [
    { title: '样本接收号', render: (_: unknown, item: RelocationRow) => item.specimen?.accessionNo || `#${item.specimenId}` },
    { title: '样本类型', render: (_: unknown, item: RelocationRow) => item.specimen?.sampleType || '-' },
    { title: '原格位', render: (_: unknown, item: RelocationRow) => item.snapshotPosition || '-' },
    { title: '处置结果', render: (_: unknown, item: RelocationRow) => item.relocatedAt
      ? <span><Tag color="blue">已转柜</Tag>{item.targetContainer?.code ? `${item.targetContainer.code} / ` : ''}{item.targetPosition}<small className="cell-subtitle">{item.relocatedByName} · {formatDateTime(item.relocatedAt)}</small></span>
      : <Tag color="default">留柜观察</Tag> },
    ...(detail?.state === 'open' ? [{
      title: '目标容器（逐支指定）',
      render: (_: unknown, item: RelocationRow) => <Select
        size="small" style={{ width: '100%' }} placeholder="选择目标容器"
        value={drafts[item.specimenId]?.targetContainerId}
        options={eligibleTargets.map((target) => ({
          value: target.id,
          label: `${target.code} · ${temperatureLabels[target.temperatureZone]} · 余 ${target.capacity - target.occupied}`,
        }))}
        onChange={(value) => setDrafts((current) => ({ ...current, [item.specimenId]: { ...current[item.specimenId], specimenId: item.specimenId, targetContainerId: value, targetPosition: undefined } }))}
      />,
    }, {
      title: '目标格位',
      render: (_: unknown, item: RelocationRow) => <Input
        size="small" placeholder="R03-B05-C07"
        value={drafts[item.specimenId]?.targetPosition}
        onChange={(event) => setDrafts((current) => ({ ...current, [item.specimenId]: { ...current[item.specimenId], specimenId: item.specimenId, targetPosition: event.target.value } }))}
      />,
    }] : []),
  ]

  return (
    <div className="page-stack">
      <header className="page-header">
        <div><Typography.Title level={2}>温度异常处置</Typography.Title>
          <Typography.Text type="secondary">冻存柜报警后登记起始温度、原因和处置人；未结案容器下样本暂缓交接与协议放行，恢复后补记结束温度与结论</Typography.Text></div>
        {can('incident:manage') && <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>登记温度异常</Button>}
      </header>
      <Row gutter={16}>
        <Col xs={24} sm={8}><div className="metric"><Statistic title="处置单总数" value={data.total} /></div></Col>
        <Col xs={24} sm={8}><div className="metric"><Statistic title="未结案" value={data.items.filter((item) => item.state === 'open').length} valueStyle={{ color: '#cf1322' }} /></div></Col>
        <Col xs={24} sm={8}><div className="metric"><Statistic title="已结案" value={data.items.filter((item) => item.state === 'resolved').length} /></div></Col>
      </Row>
      <div className="table-toolbar">
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索处置单号、报警原因或处置人" value={search} onChange={(event) => setSearch(event.target.value)} onPressEnter={() => void refresh()} />
        <Select allowClear placeholder="全部状态" value={state} onChange={setState} options={[{ value: 'open', label: '处置中' }, { value: 'resolved', label: '已结案' }]} />
        <Button onClick={() => void refresh()}>查询</Button>
      </div>
      <EntityTable columns={columns} dataSource={data.items} loading={loading} emptyTitle="暂无温度异常处置单"
        pagination={{ current: pagination.page, pageSize: pagination.pageSize, total: data.total, onChange: pagination.update, showSizeChanger: true }} />

      <Modal title="登记温度异常处置单" width={640} open={createOpen} confirmLoading={saving} onOk={() => void create()} onCancel={() => setCreateOpen(false)} okText="登记并暂缓交接放行" cancelText="取消">
        <Form form={createForm} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="containerId" label="报警容器" rules={[{ required: true }]}>
                <Select showSearch optionFilterProp="label" placeholder="选择报警冻存柜或液氮罐"
                  options={containers.filter((item) => item.active).map((item) => ({ value: item.id, label: `${item.code} · ${item.name}` }))} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="startTempC" label="报警起始温度 (°C)" rules={[{ required: true }]}>
                <InputNumber min={-200} max={40} precision={1} style={{ width: '100%' }} placeholder="如 -58.5" />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="alarmReason" label="报警原因" rules={[{ required: true, min: 3, max: 500 }]}>
            <Input.TextArea rows={3} maxLength={500} showCount placeholder="如：柜门未关严 / 压缩机故障 / 停电，说明现场初步判断" />
          </Form.Item>
          <Typography.Text type="secondary">登记后容器自动置为温度告警，容器内在管样本全部纳入处置单，未结案前其交接与协议放行将被系统拦截。</Typography.Text>
        </Form>
      </Modal>

      <Drawer width={860} title={detail ? `温度异常处置单 ${detail.incidentNo}` : '处置记录'} open={Boolean(detail)} onClose={() => setDetail(null)} extra={detail?.state === 'open' && can('incident:manage') ? (
        <Space>
          <Button icon={<SwapOutlined />} onClick={() => void submitRelocation()} loading={saving}>整单校验并转柜</Button>
          <Button type="primary" icon={<CheckCircleOutlined />} onClick={() => { setResolveTarget(detail); resolveForm.setFieldsValue({ endTempC: undefined, conclusion: '' }) }}>确认恢复结案</Button>
        </Space>
      ) : null}>
        {detail && <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {detail.state === 'open' && <Alert type="error" showIcon message="处置单未结案：容器下样本暂缓交接与协议放行" />}
          <Descriptions bordered size="small" column={2}>
            <Descriptions.Item label="报警容器">{detail.container ? `${detail.container.code} · ${detail.container.name}` : detail.containerId}</Descriptions.Item>
            <Descriptions.Item label="温区">{detail.container ? <StatusBadge value={detail.container.temperatureZone} /> : '-'}</Descriptions.Item>
            <Descriptions.Item label="状态"><StatusBadge value={detail.state} /></Descriptions.Item>
            <Descriptions.Item label="处置人">{detail.handlerName}</Descriptions.Item>
            <Descriptions.Item label="起始温度">{detail.startTempC} °C</Descriptions.Item>
            <Descriptions.Item label="结束温度">{detail.endTempC != null ? `${detail.endTempC} °C` : '待恢复'}</Descriptions.Item>
            <Descriptions.Item label="报警开始">{formatDateTime(detail.startedAt)}</Descriptions.Item>
            <Descriptions.Item label="恢复时间">{formatDateTime(detail.resolvedAt)}</Descriptions.Item>
            <Descriptions.Item label="报警原因" span={2}>{detail.alarmReason}</Descriptions.Item>
            {detail.conclusion && <Descriptions.Item label="恢复结论" span={2}>{detail.conclusion}</Descriptions.Item>}
          </Descriptions>
          <div>
            <Typography.Title level={5}>在控样本（{detail.items?.length || 0} 支）</Typography.Title>
            {detail.state === 'open' && <Typography.Paragraph type="secondary">
              仅列出温区不低于报警容器、仍有余量的可用容器；逐支指定目标容器与格位后，容量、温区、格位任一项不合适整单都不会生效。留空的样本继续留柜观察。
            </Typography.Paragraph>}
            <Table rowKey="id" size="small" pagination={false} scroll={{ x: 'max-content' }}
              dataSource={detail.items || []} columns={relocationColumns} />
          </div>
          {detail.state === 'open' && pendingItems.length === 0 && (
            <Alert type="info" showIcon message="在控样本均已完成转柜，可登记结束温度与结论后结案" />
          )}
          {detail.state === 'resolved' && <div>
            <Typography.Text type="secondary">结案后留柜样本解除管控；已转柜样本的异常时段仍保留在样本页与处置记录中。</Typography.Text>
          </div>}
        </Space>}
      </Drawer>

      <Modal title={resolveTarget ? `确认恢复 · ${resolveTarget.incidentNo}` : '确认恢复'} open={Boolean(resolveTarget)} confirmLoading={saving}
        onOk={() => void resolve()} onCancel={() => setResolveTarget(null)} okText="确认结案" cancelText="返回">
        <Form form={resolveForm} layout="vertical">
          <Form.Item name="endTempC" label="恢复后结束温度 (°C)" rules={[{ required: true }]}>
            <InputNumber min={-200} max={40} precision={1} style={{ width: '100%' }} placeholder="如 -79.2" />
          </Form.Item>
          <Form.Item name="conclusion" label="处置结论" rules={[{ required: true, min: 3, max: 1000 }]}>
            <Input.TextArea rows={4} maxLength={1000} showCount placeholder="说明设备恢复情况、样本温度复核结果和后续冻存意见" />
          </Form.Item>
          <Tag color="success">结案后报警容器恢复为可用，样本交接与协议放行解除暂缓；异常时段永久保留</Tag>
        </Form>
      </Modal>
    </div>
  )
}
