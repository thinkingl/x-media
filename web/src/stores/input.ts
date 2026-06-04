import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api'
import type { Input } from '@/api'

export const useInputStore = defineStore('input', () => {
  const inputs = ref<Input[]>([])
  const loading = ref(false)

  async function fetchInputs() {
    loading.value = true
    try {
      const { data } = await api.getInputs()
      inputs.value = data.data || []
    } catch (error) {
      console.error('Failed to fetch inputs:', error)
    } finally {
      loading.value = false
    }
  }

  async function createInput(name: string, type: string, config: string) {
    const { data } = await api.createInput({ name, type, config })
    inputs.value.push(data.data)
    return data.data
  }

  async function updateInput(id: string, name: string, type: string, config: string) {
    const { data } = await api.updateInput(id, { name, type, config })
    const idx = inputs.value.findIndex((i) => i.id === id)
    if (idx !== -1) inputs.value[idx] = data.data
    return data.data
  }

  async function deleteInput(id: string) {
    await api.deleteInput(id)
    inputs.value = inputs.value.filter((i) => i.id !== id)
  }

  async function startInput(id: string) {
    await api.startInput(id)
    const input = inputs.value.find((i) => i.id === id)
    if (input) input.status = 'running'
  }

  async function stopInput(id: string) {
    await api.stopInput(id)
    const input = inputs.value.find((i) => i.id === id)
    if (input) input.status = 'stopped'
  }

  return { inputs, loading, fetchInputs, createInput, updateInput, deleteInput, startInput, stopInput }
})
