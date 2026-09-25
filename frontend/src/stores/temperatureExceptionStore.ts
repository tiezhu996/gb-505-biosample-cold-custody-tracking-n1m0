import { useCallback, useState } from 'react'
import { temperatureExceptionAPI, type PageParams } from '../api'
import type { PageResult, TemperatureException, TemperatureExceptionState } from '../types/domain'

export function useTemperatureExceptionStore() {
  const [data, setData] = useState<PageResult<TemperatureException>>({ items: [], total: 0, page: 1, pageSize: 10 })
  const [loading, setLoading] = useState(false)
  const load = useCallback(async (params: PageParams & { state?: TemperatureExceptionState; containerId?: number } = {}) => {
    setLoading(true)
    try { setData(await temperatureExceptionAPI.list(params)) } finally { setLoading(false) }
  }, [])
  return { data, loading, load }
}
