import { AlertOutlined, CheckCircleOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons'
import {
  Alert, Button, Col, Descriptions, Drawer, Form, Input, InputNumber, Radio, Row, Select, Space, Statistic,
  Table, Typography, message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useEffect, useMemo, useState } from 'react'
import { specimenAPI, storageAPI, temperatureExceptionAPI } from '../api'
import { EntityTable } from '../components/common/EntityTable'
import { StatusBadge } from '../components/common/StatusBadge'
import { useAuth } from '../hooks/useAuth'
import { usePagination } from '../hooks/usePagination'
import { useTemperatureExceptionStore } from '../stores/temperatureExceptionStore'
import type {
  Specimen, StorageContainer, TemperatureException, TemperatureExceptionState,
} from '../types/domain'
import { formatDateTime, temperatureLabels } from '../utils/format'

type DraftItem = { specimenId: number; targetContainerId?: number; targetPosition?: string }

export function TemperatureExceptionsPage() {
  const { data, loading, load } = useTemperatureExceptionStore()
  const pagination = usePagination()
  const { can } = useAuth()
  const [search, setSearch] = useState('')
  const [state, setState] = useState<TemperatureExceptionState>()
  const [containers, setContainers] = useState<StorageContainer[]>([])
  const [specimens, setSpecimens] = useState<Specimen[]>([])
  const [openForm] = Form.useForm()
  const [closeForm] = Form.useForm()
  const [createOpen, setCreateOpen] = useState(false)
  const [closeTarget, setCloseTarget] = useState<TemperatureException | null>(null)
  const [detail, setDetail] = useState<TemperatureException | null>(null)
  const [saving, setSaving] = useState(false)
  const [action, setAction] = useState<'onsite' | 'relocation'>('onsite')
  const [sourceContainerId, setSourceContainerId] = useState<number>()
  const [items, setItems] = useState<DraftItem[]>([])
  const canHandle = can('temperature:handle')

  const refresh = () => load({ page: pagination.page, pageSize: pagination.pageSize, search, state })
  useEffect(() => { void refresh() }, [pagination.page, pagination.pageSize, state])
  useEffect(() => {
    void storageAPI.list({ page: 1, pageSize: 100 }).then((result) => setContainers(result.items))
    void specimenAPI.list({ page: 1, pageSize: 100 }).then((result) => setSpecimens(result.items))
  }, [])

  const sourceContainer = useMemo(
    () => containers.find((item) => item.id === sourceContainerId),
    [containers, sourceContainerId],
  )

  const specimenLabel = (id: number) => {
    const row = specimens.find((item) => item.id === id)
    return row ? `${row.accessionNo} · ${row.sampleType}（原格位 ${row.position || '-'}）` : `#${id}`
  }

  const chooseSource = async (containerId: number) => {
    setSourceContainerId(containerId)
    const result = await specimenAPI.list({ page: 1, pageSize: 100, storageContainerId: containerId })
    setSpecimens((current) => {
      const merged = [...current]
      for (const specimen of result.items) {
        if (!merged.some((item) => item.id === specimen.id)) merged.push(specimen)
      }
      return merged
    })
    setItems(result.items.filter((item) => item.state === 'stored').map((item) => ({ specimenId: item.id })))
  }

  const updateItem = (index: number, patch: Partial<DraftItem>) => {
    setItems((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item))
  }

  const targetContainers = containers.filter((item) =>
    item.active && item.status === 'available'
    && item.temperatureZone === sourceContainer?.temperatureZone
    && item.id !== sourceContainerId)

  const openCount = data.items.filter((item) => item.state === 'open').length

  const submitOpen = async () => {
    const values = await openForm.validateFields()
    if (action === 'relocation') {
      if (!items.length) {
        message.error('容器内没有在柜样本，不能选择转柜处置')
        return
      }
      const missing = items.some((item) => !item.targetContainerId || !item.targetPosition?.trim())
      if (missing) {
        message.error('转柜处置必须为每一支样本指定目标容器和格位')
        return
      }
      const seen = new Set<string>()
      for (const item of items) {
        const key = `${item.targetContainerId}@${item.targetPosition!.trim()}`
        if (seen.has(key)) {
          message.error(`目标格位 ${item.targetPosition} 在本单中重复指定，整单不会生效`)
          return
        }
        seen.add(key)
      }
    }
    if (!items.length) {
      message.error('该容器当前没有在柜样本，无需登记温度异常处置单')
      return
    }
    setSaving(true)
    try {
      await temperatureExceptionAPI.open({
        exceptionNo: values.exceptionNo,
        containerId: values.containerId,
        startTemperatureC: values.startTemperatureC,
        alarmReason: values.alarmReason,
        action,
        items: action === 'relocation'
          ? items.map((item) => ({
            specimenId: item.specimenId,
            targetContainerId: item.targetContainerId,
            targetPosition: item.targetPosition?.trim(),
          }))
          : items.map((item) => ({ specimenId: item.specimenId })),
      })
      message.success('温度异常处置单已登记，相关样本已暂缓交接与放行')
      setCreateOpen(false)
      openForm.resetFields()
      setItems([])
      setSourceContainerId(undefined)
      await refresh()
      const storage = await storageAPI.list({ page: 1, pageSize: 100 })
      setContainers(storage.items)
    } finally { setSaving(false) }
  }

  const submitClose = async () => {
    if (!closeTarget) return
    const values = await closeForm.validateFields()
    setSaving(true)
    try {
      await temperatureExceptionAPI.close(closeTarget.id, values)
      message.success('处置单已结案，容器与样本限制已恢复')
      setCloseTarget(null)
      closeForm.resetFields()
      await refresh()
    } finally { setSaving(false) }
  }

  const showDetail = async (row: TemperatureException) => setDetail(await temperatureExceptionAPI.get(row.id))

  const columns: ColumnsType<TemperatureException> = [
    { title: '处置单号', dataIndex: 'exceptionNo', fixed: 'left', render: (value, row) => <Button type="link" className="table-link" onClick={() => void showDetail(row)}>{value}</Button> },
    { title: '容器', render: (_, row) => <div><strong>{row.container?.code || row.containerId}</strong><small className="cell-subtitle">{row.container?.name}</small></div> },
    { title: '状态', dataIndex: 'state', render: (value) => <StatusBadge value={value} dot /> },
    { title: '处置方式', dataIndex: 'action', render: (value) => <StatusBadge value={value} /> },
    { title: '温度变化', render: (_, row) => <div>{row.startTemperatureC}°C{row.endTemperatureC != null ? <small className="cell-subtitle">→ {row.endTemperatureC}°C</small> : <small className="cell-subtitle">恢复中…</small>}</div> },
    { title: '受影响样本', render: (_, row) => row.items?.length ?? 0 },
    { title: '报警原因', dataIndex: 'alarmReason', ellipsis: true },
    { title: '处置人', dataIndex: 'handlerName' },
    { title: '报警/结案时间', render: (_, row) => <div>{formatDateTime(row.startedAt)}<small className="cell-subtitle">{row.closedAt ? formatDateTime(row.closedAt) : '未结案'}</small></div> },
    {
      title: '操作', fixed: 'right',
      render: (_, row) => (
        <Space>
          <Button size="small" onClick={() => void showDetail(row)}>详情</Button>
          {row.state === 'open' && canHandle && (
            <Button size="small" type="primary" icon={<CheckCircleOutlined />} onClick={() => { setCloseTarget(row); closeForm.setFieldsValue({ outcome: 'recovered' }) }}>确认恢复结案</Button>
          )}
        </Space>
      ),
    },
  ]

  const draftColumns: ColumnsType<DraftItem> = [
    { title: '样本', render: (_, row) => specimenLabel(row.specimenId) },
    {
      title: '目标容器（同温区）',
      render: (_, row, index) => action === 'relocation' ? (
        <Select
          style={{ width: '100%' }} showSearch optionFilterProp="label"
          placeholder="逐支选择目标容器"
          value={row.targetContainerId}
          onChange={(value) => updateItem(index, { targetContainerId: value })}
          options={targetContainers.map((item) => ({
            value: item.id,
            label: `${item.code} · ${item.name}（剩余 ${item.capacity - item.occupied} 格）`,
            disabled: item.occupied >= item.capacity,
          }))}
        />
      ) : '—',
    },
    {
      title: '目标格位',
      render: (_, row, index) => action === 'relocation'
        ? <Input placeholder="如 R02-BX04-A03" value={row.targetPosition} onChange={(event) => updateItem(index, { targetPosition: event.target.value })} />
        : '—',
    },
  ]

  return (
    <div className="page-stack">
      <header className="page-header">
        <div>
          <Typography.Title level={2}>温度异常处置</Typography.Title>
          <Typography.Text type="secondary">
            登记冻存柜报警起始温度、原因与处置人；未结案期间容器内样本暂缓交接与协议放行，转柜须逐支指定目标格位
          </Typography.Text>
        </div>
        {canHandle && <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>登记温度异常</Button>}
      </header>
      <Row gutter={16}>
        <Col xs={24} sm={8}><div className="metric"><Statistic title="处置单总数" value={data.total} /></div></Col>
        <Col xs={24} sm={8}><div className="metric"><Statistic title="未结案" value={openCount} prefix={<AlertOutlined />} valueStyle={{ color: openCount ? '#cf1322' : undefined }} /></div></Col>
        <Col xs={24} sm={8}><div className="metric"><Statistic title="已结案" value={data.items.filter((item) => item.state === 'closed').length} /></div></Col>
      </Row>
      {openCount > 0 && <Alert type="error" showIcon message="存在未结案温度异常处置单" description="受影响样本不能发起/受理交接，协议复核也不能批准放行；请在温度恢复后及时补录结束温度与结论并结案。" />}
      <div className="table-toolbar">
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索处置单号、原因或处置人" value={search} onChange={(event) => setSearch(event.target.value)} onPressEnter={() => void refresh()} />
        <Select allowClear placeholder="全部状态" value={state} onChange={setState} options={[{ value: 'open', label: '未结案' }, { value: 'closed', label: '已结案' }]} />
        <Button onClick={() => void refresh()}>查询</Button>
      </div>
      <EntityTable
        columns={columns} dataSource={data.items} loading={loading} emptyTitle="暂无温度异常处置单"
        pagination={{ current: pagination.page, pageSize: pagination.pageSize, total: data.total, onChange: pagination.update, showSizeChanger: true }}
      />

      <Drawer
        width={900} title="登记温度异常处置单" open={createOpen} onClose={() => setCreateOpen(false)}
        extra={<Space><Button onClick={() => setCreateOpen(false)}>取消</Button><Button type="primary" loading={saving} onClick={() => void submitOpen()}>登记处置单</Button></Space>}>
        <Alert type="info" showIcon style={{ marginBottom: 16 }}
          message="处置单登记后容器立即进入“温度告警”；未结案前，容器内样本暂缓交接与协议放行。选择转柜时，任一支样本的容量、温区或格位校验失败都会导致整单不生效。" />
        <Form form={openForm} layout="vertical">
          <Row gutter={16}>
            <Col span={12}><Form.Item name="exceptionNo" label="处置单号" rules={[{ required: true, min: 3 }]}><Input placeholder="TE-20260925-001" /></Form.Item></Col>
            <Col span={12}>
              <Form.Item name="containerId" label="报警容器" rules={[{ required: true }]}>
                <Select showSearch optionFilterProp="label" placeholder="选择发生报警的冻存柜/液氮罐"
                  onChange={(value) => void chooseSource(value)}
                  options={containers.map((item) => ({ value: item.id, label: `${item.code} · ${item.name}（${temperatureLabels[item.temperatureZone]}）` }))} />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={16}>
            <Col span={8}><Form.Item name="startTemperatureC" label="起始温度 (°C)" rules={[{ required: true, message: '填写报警时实测温度' }]}><InputNumber min={-210} max={40} precision={1} style={{ width: '100%' }} /></Form.Item></Col>
            <Col span={16}>
              <Form.Item label="处置方式" required>
                <Radio.Group value={action} onChange={(event) => setAction(event.target.value)}
                  options={[{ value: 'onsite', label: '现场观察/抢修，样本留柜' }, { value: 'relocation', label: '转柜，逐支指定目标格位' }]} />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="alarmReason" label="报警原因" rules={[{ required: true, min: 3 }]}>
            <Input.TextArea rows={2} maxLength={1000} showCount placeholder="如：柜门未关严、压缩机制冷异常、液氮补液延迟" />
          </Form.Item>
          <Typography.Text strong>受影响样本（自动取自容器内在柜样本，共 {items.length} 支）</Typography.Text>
          {sourceContainer && <Typography.Paragraph type="secondary">源容器温区：{temperatureLabels[sourceContainer.temperatureZone]}；目标容器必须为同温区、可用且容量充足。</Typography.Paragraph>}
          <Table style={{ marginTop: 8 }} size="small" rowKey={(row) => String(row.specimenId)} pagination={false}
            dataSource={items} columns={draftColumns} locale={{ emptyText: '该容器当前没有在柜样本' }} />
        </Form>
      </Drawer>

      <Drawer width={720} title={closeTarget ? `确认恢复 · ${closeTarget.exceptionNo}` : '确认恢复'} open={Boolean(closeTarget)} onClose={() => setCloseTarget(null)}
        extra={<Space><Button onClick={() => setCloseTarget(null)}>取消</Button><Button type="primary" loading={saving} onClick={() => void submitClose()}>确认结案</Button></Space>}>
        {closeTarget && <>
          <Alert type="warning" showIcon style={{ marginBottom: 16 }} message="结案后该容器内样本将恢复交接与协议放行；若设备无法恢复，请选择“设备停用待修”，容器将转为维护中。" />
          <Descriptions bordered size="small" column={1}>
            <Descriptions.Item label="报警容器">{closeTarget.container?.code} · {closeTarget.container?.name}</Descriptions.Item>
            <Descriptions.Item label="起始温度">{closeTarget.startTemperatureC}°C</Descriptions.Item>
            <Descriptions.Item label="温区要求">{temperatureLabels[closeTarget.container?.temperatureZone || ''] || closeTarget.container?.temperatureZone}</Descriptions.Item>
            <Descriptions.Item label="报警原因">{closeTarget.alarmReason}</Descriptions.Item>
          </Descriptions>
          <Form form={closeForm} layout="vertical" style={{ marginTop: 16 }}>
            <Form.Item name="endTemperatureC" label="结束温度 (°C)" rules={[{ required: true, message: '请填写恢复后的实测温度' }]}>
              <InputNumber min={-210} max={40} precision={1} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="outcome" label="处置结论" rules={[{ required: true }]}>
              <Radio.Group options={[{ value: 'recovered', label: '温度恢复，样本可继续使用' }, { value: 'discarded', label: '设备无法恢复，转维护停用' }]} />
            </Form.Item>
            <Form.Item name="conclusionNotes" label="结论说明" rules={[{ required: true, min: 3 }]}>
              <Input.TextArea rows={4} maxLength={2000} showCount placeholder="记录温度恢复过程、样本影响评估与后续观察措施" />
            </Form.Item>
          </Form>
        </>}
      </Drawer>

      <Drawer width={760} title={detail ? `处置单 ${detail.exceptionNo}` : '处置单详情'} open={Boolean(detail)} onClose={() => setDetail(null)}>
        {detail && <>
          <Descriptions bordered size="small" column={{ xs: 1, md: 2 }}>
            <Descriptions.Item label="状态"><StatusBadge value={detail.state} /></Descriptions.Item>
            <Descriptions.Item label="处置方式"><StatusBadge value={detail.action} /></Descriptions.Item>
            <Descriptions.Item label="容器">{detail.container?.code} · {detail.container?.name}</Descriptions.Item>
            <Descriptions.Item label="温区">{detail.container ? <StatusBadge value={detail.container.temperatureZone} /> : '-'}</Descriptions.Item>
            <Descriptions.Item label="起始温度">{detail.startTemperatureC}°C</Descriptions.Item>
            <Descriptions.Item label="结束温度">{detail.endTemperatureC != null ? `${detail.endTemperatureC}°C` : '未结案'}</Descriptions.Item>
            <Descriptions.Item label="报警时间">{formatDateTime(detail.startedAt)}</Descriptions.Item>
            <Descriptions.Item label="结案时间">{detail.closedAt ? formatDateTime(detail.closedAt) : '未结案'}</Descriptions.Item>
            <Descriptions.Item label="处置人">{detail.handlerName}</Descriptions.Item>
            <Descriptions.Item label="结案人">{detail.closedByName || '-'}</Descriptions.Item>
            <Descriptions.Item label="报警原因" span={2}>{detail.alarmReason}</Descriptions.Item>
            {detail.conclusionNotes && <Descriptions.Item label="结案结论" span={2}>{detail.conclusionNotes}</Descriptions.Item>}
          </Descriptions>
          <Typography.Title level={5} style={{ marginTop: 16 }}>受影响样本与格位</Typography.Title>
          <Table size="small" rowKey={(row) => row.id} pagination={false} dataSource={detail.items || []} columns={[
            { title: '样本', render: (_, row) => row.specimen?.accessionNo || row.specimenId },
            { title: '原格位', dataIndex: 'sourcePosition' },
            { title: '是否转柜', dataIndex: 'moved', render: (value) => value ? '已转柜' : '现场观察' },
            { title: '目标格位', render: (_, row) => row.targetContainer ? `${row.targetContainer.code} / ${row.targetPosition}` : '-' },
            { title: '备注', dataIndex: 'notes' },
          ]} />
        </>}
      </Drawer>
    </div>
  )
}
