import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api'
import type { Output } from '@/api'

export const useOutputStore = defineStore('output', () => {
  const outputs = ref<Output[]>([])
  const loading = ref(false)

  async function fetchOutputs() {
    loading.value = true
    try {
      const { data } = await api.getOutputs()
      outputs.value = data.data || []
    } catch (error) {
      console.error('Failed to fetch outputs:', error)
    } finally {
      loading.value = false
    }
  }

  async function createOutput(name: string, type: string, config: string) {
    const { data } = await api.createOutput({ name, type, config })
    outputs.value.push(data.data)
    return data.data
  }

  async function deleteOutput(id: string) {
    await api.deleteOutput(id)
    outputs.value = outputs.value.filter((o) => o.id !== id)
  }

  async function startOutput(id: string) {
    await api.startOutput(id)
    const output = outputs.value.find((o) => o.id === id)
    if (output) output.status = 'running'
  }

  async function stopOutput(id: string) {
    await api.stopOutput(id)
    const output = outputs.value.find((o) => o.id === id)
    if (output) output.status = 'stopped'
  }

  return { outputs, loading, fetchOutputs, createOutput, deleteOutput, startOutput, stopOutput }
})
