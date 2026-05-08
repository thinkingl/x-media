import axios from 'axios'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 10000,
})

export interface Input {
  id: string
  name: string
  type: string
  config: string
  status: string
  created_at: string
  updated_at: string
}

export interface Output {
  id: string
  name: string
  type: string
  config: string
  status: string
  created_at: string
  updated_at: string
}

export interface Pipe {
  id: string
  input_id: string
  output_id: string
  status: string
  created_at: string
  updated_at: string
}

export interface Stats {
  total_bytes_in: number
  total_bytes_out: number
  active_inputs: number
  active_outputs: number
  active_pipes: number
}

export interface LogEntry {
  level: string
  message: string
  timestamp: string
}

export interface LogConfig {
  level: string
  filename: string
  max_size: number
  max_backups: number
  max_age: number
  compress: boolean
}

// Inputs
export const getInputs = () => api.get<Input[]>('/inputs')
export const getInput = (id: string) => api.get<Input>(`/inputs/${id}`)
export const createInput = (data: { name: string; type: string; config: string }) =>
  api.post<Input>('/inputs', data)
export const updateInput = (id: string, data: { name: string; type: string; config: string }) =>
  api.put<Input>(`/inputs/${id}`, data)
export const deleteInput = (id: string) => api.delete(`/inputs/${id}`)
export const startInput = (id: string) => api.post(`/inputs/${id}/start`)
export const stopInput = (id: string) => api.post(`/inputs/${id}/stop`)

// Outputs
export const getOutputs = () => api.get<Output[]>('/outputs')
export const getOutput = (id: string) => api.get<Output>(`/outputs/${id}`)
export const createOutput = (data: { name: string; type: string; config: string }) =>
  api.post<Output>('/outputs', data)
export const updateOutput = (id: string, data: { name: string; type: string; config: string }) =>
  api.put<Output>(`/outputs/${id}`, data)
export const deleteOutput = (id: string) => api.delete(`/outputs/${id}`)
export const startOutput = (id: string) => api.post(`/outputs/${id}/start`)
export const stopOutput = (id: string) => api.post(`/outputs/${id}/stop`)

// Pipes
export const getPipes = () => api.get<Pipe[]>('/pipes')
export const getPipe = (id: string) => api.get<Pipe>(`/pipes/${id}`)
export const createPipe = (data: { input_id: string; output_id: string }) =>
  api.post<Pipe>('/pipes', data)
export const deletePipe = (id: string) => api.delete(`/pipes/${id}`)
export const startPipe = (id: string) => api.post(`/pipes/${id}/start`)
export const stopPipe = (id: string) => api.post(`/pipes/${id}/stop`)

// Stats
export const getStats = () => api.get<Stats>('/stats')

// Logs
export const getLogs = (lines?: number) => api.get<LogEntry[]>('/logs', { params: { lines } })
export const getLogConfig = () => api.get<LogConfig>('/logs/config')
export const updateLogConfig = (config: Partial<LogConfig>) => api.put<LogConfig>('/logs/config', config)

export default api
