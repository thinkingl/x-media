import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api'
import type { Pipe } from '@/api'

export const usePipeStore = defineStore('pipe', () => {
  const pipes = ref<Pipe[]>([])
  const loading = ref(false)

  async function fetchPipes() {
    loading.value = true
    try {
      const { data } = await api.getPipes()
      pipes.value = data.data || []
    } catch (error) {
      console.error('Failed to fetch pipes:', error)
    } finally {
      loading.value = false
    }
  }

  async function createPipe(inputId: string, outputId: string) {
    const { data } = await api.createPipe({ input_id: inputId, output_id: outputId })
    pipes.value.push(data.data)
    return data.data
  }

  async function deletePipe(id: string) {
    await api.deletePipe(id)
    pipes.value = pipes.value.filter((p) => p.id !== id)
  }

  async function startPipe(id: string) {
    await api.startPipe(id)
    const pipe = pipes.value.find((p) => p.id === id)
    if (pipe) pipe.status = 'running'
  }

  async function stopPipe(id: string) {
    await api.stopPipe(id)
    const pipe = pipes.value.find((p) => p.id === id)
    if (pipe) pipe.status = 'stopped'
  }

  return { pipes, loading, fetchPipes, createPipe, deletePipe, startPipe, stopPipe }
})
