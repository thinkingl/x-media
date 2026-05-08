import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api'
import type { Stats } from '@/api'

export const useStatsStore = defineStore('stats', () => {
  const stats = ref<Stats | null>(null)
  const loading = ref(false)

  async function fetchStats() {
    loading.value = true
    try {
      const { data } = await api.getStats()
      stats.value = data.data
    } catch (error) {
      console.error('Failed to fetch stats:', error)
    } finally {
      loading.value = false
    }
  }

  return { stats, loading, fetchStats }
})
