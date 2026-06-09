import axios from 'axios'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 10000,
})

export interface ApiResponse<T> {
  code: number
  message: string
  data: T
}

export interface Input {
  id: string
  name: string
  type: string
  config: string
  media_info?: string
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
export interface StreamInfo {
  index: number
  codec_type: string
  codec_name: string
  codec_long_name: string
  width?: number
  height?: number
  profile?: string
  pix_fmt?: string
  sample_rate?: string
  channels?: number
  channel_layout?: string
  bit_rate?: string
}

export interface MediaInfo {
  file_name: string
  file_path: string
  file_size: number
  duration: number
  bit_rate: number
  format_name: string
  format_long_name: string
  streams: StreamInfo[]
  thumbnail_path?: string
}

export const getInputs = () => api.get<ApiResponse<Input[]>>('/inputs')
export const getInput = (id: string) => api.get<ApiResponse<Input>>(`/inputs/${id}`)
export const createInput = (data: { name: string; type: string; config: string }) =>
  api.post<ApiResponse<Input>>('/inputs', data)
export const updateInput = (id: string, data: { name: string; type: string; config: string }) =>
  api.put<ApiResponse<Input>>(`/inputs/${id}`, data)
export const deleteInput = (id: string) => api.delete(`/inputs/${id}`)
export const startInput = (id: string) => api.post(`/inputs/${id}/start`)
export const stopInput = (id: string) => api.post(`/inputs/${id}/stop`)
export const probeInput = (id: string) => api.post<ApiResponse<MediaInfo>>(`/inputs/${id}/probe`)

// Outputs
export const getOutputs = () => api.get<ApiResponse<Output[]>>('/outputs')
export const getOutput = (id: string) => api.get<ApiResponse<Output>>(`/outputs/${id}`)
export const createOutput = (data: { name: string; type: string; config: string }) =>
  api.post<ApiResponse<Output>>('/outputs', data)
export const updateOutput = (id: string, data: { name: string; type: string; config: string }) =>
  api.put<ApiResponse<Output>>(`/outputs/${id}`, data)
export const deleteOutput = (id: string) => api.delete(`/outputs/${id}`)
export const startOutput = (id: string) => api.post(`/outputs/${id}/start`)
export const stopOutput = (id: string) => api.post(`/outputs/${id}/stop`)

// Pipes
export const getPipes = () => api.get<ApiResponse<Pipe[]>>('/pipes')
export const getPipe = (id: string) => api.get<ApiResponse<Pipe>>(`/pipes/${id}`)
export const createPipe = (data: { input_id: string; output_id: string; channel_map?: string; mux_sync?: boolean }) =>
  api.post<ApiResponse<Pipe>>('/pipes', data)
export const updatePipe = (id: string, data: { input_id: string; output_id: string; channel_map?: string; mux_sync?: boolean }) =>
  api.put<ApiResponse<Pipe>>(`/pipes/${id}`, data)
export const deletePipe = (id: string) => api.delete(`/pipes/${id}`)
export const startPipe = (id: string) => api.post(`/pipes/${id}/start`)
export const stopPipe = (id: string) => api.post(`/pipes/${id}/stop`)

// Stats
export const getStats = () => api.get<ApiResponse<Stats>>('/stats')

// Logs
export const getLogs = (lines?: number) => api.get<ApiResponse<LogEntry[]>>('/logs', { params: { lines } })
export const getLogConfig = () => api.get<ApiResponse<LogConfig>>('/logs/config')
export const updateLogConfig = (config: Partial<LogConfig>) => api.put<ApiResponse<LogConfig>>('/logs/config', config)

export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
}

export const listFiles = (dirPath: string) =>
  api.get<ApiResponse<FileEntry[]>>('/files/list', { params: { path: dirPath } })

export const uploadFile = (file: File) => {
  const formData = new FormData()
  formData.append('file', file)
  return api.post<ApiResponse<{ path: string }>>('/files/upload', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export default api
