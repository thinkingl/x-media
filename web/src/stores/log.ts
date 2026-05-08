import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api'
import type { LogEntry, LogConfig } from '@/api'

export const useLogStore = defineStore('log', () => {
  const logs = ref<LogEntry[]>([])
  const logConfig = ref<LogConfig | null>(null)
  const loading = ref(false)

  async function fetchLogs(lines?: number) {
    loading.value = true
    try {
      const { data } = await api.getLogs(lines)
      logs.value = data.data || []
    } catch (error) {
      console.error('Failed to fetch logs:', error)
    } finally {
      loading.value = false
    }
  }

  async function fetchLogConfig() {
    try {
      const { data } = await api.getLogConfig()
      logConfig.value = data.data
    } catch (error) {
      console.error('Failed to fetch log config:', error)
    }
  }

  async function updateLogConfig(config: Partial<LogConfig>) {
    const { data } = await api.updateLogConfig(config)
    logConfig.value = data.data
    return data.data
  }

  return { logs, logConfig, loading, fetchLogs, fetchLogConfig, updateLogConfig }
})
