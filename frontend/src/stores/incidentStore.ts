import { useCallback, useState } from 'react'
import { incidentAPI, type PageParams } from '../api'
import type { IncidentState, PageResult, TemperatureIncident } from '../types/domain'

const empty: PageResult<TemperatureIncident> = { items: [], total: 0, page: 1, pageSize: 10 }

export function useIncidentStore() {
  const [data, setData] = useState<PageResult<TemperatureIncident>>(empty)
  const [loading, setLoading] = useState(false)
  const load = useCallback(async (params: PageParams & { state?: IncidentState; containerId?: number } = {}) => {
    setLoading(true)
    try { setData(await incidentAPI.list(params)) } finally { setLoading(false) }
  }, [])
  return { data, loading, load }
}
