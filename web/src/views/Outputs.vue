<template>
  <div class="outputs-view">
    <div class="page-header">
      <h2>输出端管理</h2>
      <el-button type="primary" @click="showCreateDialog">创建输出端</el-button>
    </div>

    <el-table :data="outputStore.outputs" v-loading="outputStore.loading" stripe>
      <el-table-column prop="id" label="ID" width="120" />
      <el-table-column prop="name" label="名称" />
      <el-table-column prop="type" label="类型" width="120">
        <template #default="{ row }">
          <el-tag :type="getTypeTag(row.type)">{{ row.type }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="流地址" min-width="200">
        <template #default="{ row }">
          <template v-if="getStreamUrl(row)">
            <span class="stream-url">{{ getStreamUrl(row) }}</span>
            <el-button link type="primary" size="small" @click="copyUrl(getStreamUrl(row))">复制</el-button>
          </template>
          <span v-else class="text-muted">-</span>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'running' ? 'success' : 'info'">
            {{ row.status === 'running' ? '运行中' : '已停止' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="300">
        <template #default="{ row }">
          <el-button type="info" size="small" @click="showDetails(row)">详情</el-button>
          <el-button type="primary" size="small" @click="showEditDialog(row)" :disabled="row.status === 'running'">编辑</el-button>
          <el-button v-if="row.status !== 'running'" type="success" size="small" @click="handleStart(row.id)">启动</el-button>
          <el-button v-if="row.status === 'running'" type="warning" size="small" @click="handleStop(row.id)">停止</el-button>
          <el-button type="danger" size="small" @click="handleDelete(row.id)" :disabled="row.status === 'running'">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="isEditing ? '编辑输出端' : '创建输出端'" width="500">
      <el-form :model="form" label-width="120px">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="请输入名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.type" placeholder="请选择类型" :disabled="isEditing">
            <el-option label="RTMP" value="rtmp" />
            <el-option label="RTSP" value="rtsp" />
            <el-option label="HTTP-FLV" value="http-flv" />
            <el-option label="WebRTC" value="webrtc" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.type === 'rtmp'" label="RTMP URL">
          <el-input v-model="form.configUrl" placeholder="rtmp://live.example.com/live/key" />
        </el-form-item>
        <el-form-item v-if="form.type === 'rtsp'" label="模式">
          <el-select v-model="form.rtspMode">
            <el-option label="推流" value="push" />
            <el-option label="服务" value="server" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.type === 'rtsp' && form.rtspMode === 'push'" label="RTSP URL">
          <el-input v-model="form.configUrl" placeholder="rtsp://example.com/stream" />
        </el-form-item>
        <el-form-item v-if="form.type === 'rtsp' && form.rtspMode === 'server'" label="监听地址">
          <el-input v-model="form.configAddr" placeholder=":5544" />
        </el-form-item>
        <el-form-item v-if="form.type === 'rtsp' && form.rtspMode === 'server'" label="传输协议">
          <el-select v-model="form.rtspTransport">
            <el-option label="自动适配" value="auto" />
            <el-option label="TCP" value="tcp" />
            <el-option label="UDP" value="udp" />
            <el-option label="UDP 组播" value="udp-multicast" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.type === 'http-flv'" label="访问路径">
          <el-input v-model="form.configAddr" placeholder="/live/:id.flv（固定，无需修改）" disabled />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSubmit" :loading="submitting">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailsVisible" title="输出端详情" width="600">
      <div v-if="currentOutput">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="ID">{{ currentOutput.id }}</el-descriptions-item>
          <el-descriptions-item label="名称">{{ currentOutput.name }}</el-descriptions-item>
          <el-descriptions-item label="类型">
            <el-tag :type="getTypeTag(currentOutput.type)">{{ currentOutput.type }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="currentOutput.status === 'running' ? 'success' : 'info'">
              {{ currentOutput.status === 'running' ? '运行中' : '已停止' }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>

        <div class="config-section">
          <h4>配置信息</h4>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item v-if="currentStreamUrl" label="流地址">
              <div class="url-row">
                <span class="url-text">{{ currentStreamUrl }}</span>
                <el-button link type="primary" size="small" @click="copyUrl(currentStreamUrl)">复制</el-button>
              </div>
            </el-descriptions-item>
            <el-descriptions-item v-if="parsedConfig.url" label="推流地址">
              <div class="url-row">
                <span class="url-text">{{ parsedConfig.url }}</span>
                <el-button link type="primary" size="small" @click="copyUrl(parsedConfig.url)">复制</el-button>
              </div>
            </el-descriptions-item>
            <el-descriptions-item v-if="parsedConfig.mode" label="模式">{{ parsedConfig.mode === 'push' ? '推流' : '服务' }}</el-descriptions-item>
            <el-descriptions-item v-if="parsedConfig.addr && currentOutput && currentOutput.type !== 'http-flv'" label="监听地址">{{ parsedConfig.addr }}</el-descriptions-item>
            <el-descriptions-item v-if="parsedConfig.transport" label="传输协议">{{ transportLabel(parsedConfig.transport) }}</el-descriptions-item>
          </el-descriptions>
        </div>

        <div class="config-section">
          <h4>原始配置</h4>
          <pre class="raw-config">{{ currentOutput.config }}</pre>
        </div>

        <div class="config-section">
          <h4>当前客户端 ({{ clients.length }})</h4>
          <el-table :data="clients" v-loading="clientsLoading" size="small" empty-text="暂无客户端连接">
            <el-table-column prop="address" label="地址" min-width="140">
              <template #default="{ row }">
                <span class="mono">{{ row.address }}</span>
              </template>
            </el-table-column>
            <el-table-column prop="user_agent" label="客户端" min-width="120" />
            <el-table-column prop="transport" label="传输" width="70">
              <template #default="{ row }">
                <el-tag :type="row.transport === 'tcp' ? 'success' : 'warning'" size="small">{{ row.transport }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="connected_at" label="连接时间" width="160">
              <template #default="{ row }">
                <span class="mono">{{ formatTime(row.connected_at) }}</span>
              </template>
            </el-table-column>
          </el-table>
          <div class="clients-actions">
            <el-button size="small" :loading="clientsLoading" @click="loadClients">刷新</el-button>
          </div>
        </div>
      </div>
      <template #footer>
        <el-button @click="detailsVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useOutputStore } from '@/stores/output'
import { getOutputClients } from '@/api'
import type { ClientInfo, Output } from '@/api'

const outputStore = useOutputStore()
const dialogVisible = ref(false)
const detailsVisible = ref(false)
const isEditing = ref(false)
const editingId = ref('')
const submitting = ref(false)
const currentOutput = ref<Output | null>(null)
const clients = ref<ClientInfo[]>([])
const clientsLoading = ref(false)

const form = reactive({
  name: '',
  type: 'rtmp',
  configUrl: '',
  configAddr: '',
  rtspMode: 'push',
  rtspTransport: 'auto',
})

const parsedConfig = computed(() => {
  if (!currentOutput.value) return {}
  try {
    return JSON.parse(currentOutput.value.config)
  } catch {
    return {}
  }
})

const currentStreamUrl = computed(() => {
  if (!currentOutput.value) return ''
  return getStreamUrl(currentOutput.value)
})

onMounted(() => {
  outputStore.fetchOutputs()
})

function getTypeTag(type: string) {
  const tags: Record<string, string> = { rtmp: 'danger', rtsp: 'warning', 'http-flv': 'success', hls: '' }
  return tags[type] || 'info'
}

function transportLabel(t: string): string {
  const labels: Record<string, string> = { auto: '自动适配', tcp: 'TCP', udp: 'UDP', 'udp-multicast': 'UDP 组播' }
  return labels[t] || t
}

function getStreamUrl(row: Output): string {
  try {
    const cfg = JSON.parse(row.config)
    if (row.type === 'rtmp' && cfg.url) return cfg.url
    if (row.type === 'rtsp' && cfg.mode === 'server' && cfg.addr) {
      const host = location.hostname
      const port = cfg.addr.replace(':', '')
      return `rtsp://${host}:${port}/live/${row.id}`
    }
    if (row.type === 'rtsp' && cfg.mode === 'push' && cfg.url) return cfg.url
    if (row.type === 'http-flv') {
      // HTTP-FLV 挂在 API server 上（无独立监听端口），使用当前页面地址。
      // 生产：前端与 API 同端口(18090)；dev：vite 已代理 /live 到 18090。
      return `http://${location.host}/live/${row.id}.flv`
    }
    if (row.type === 'webrtc') {
      // WHEP 信令端点（浏览器 RTCPeerConnection 播放）
      return `http://${location.host}/live/${row.id}/whep`
    }
  } catch {}
  return ''
}

function copyUrl(url: string) {
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(url).then(() => {
      ElMessage.success('已复制到剪贴板')
    }).catch(() => {
      fallbackCopy(url)
    })
  } else {
    fallbackCopy(url)
  }
}

function fallbackCopy(text: string) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.left = '-9999px'
  document.body.appendChild(ta)
  ta.select()
  try {
    document.execCommand('copy')
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.error('复制失败')
  }
  document.body.removeChild(ta)
}

function resetForm() {
  form.name = ''
  form.type = 'rtmp'
  form.configUrl = ''
  form.configAddr = ''
  form.rtspMode = 'push'
  form.rtspTransport = 'auto'
}

function showCreateDialog() {
  isEditing.value = false
  editingId.value = ''
  resetForm()
  dialogVisible.value = true
}

function showEditDialog(row: Output) {
  isEditing.value = true
  editingId.value = row.id
  form.name = row.name
  form.type = row.type
  try {
    const cfg = JSON.parse(row.config)
    form.configUrl = cfg.url || ''
    form.configAddr = cfg.addr || ''
    form.rtspMode = cfg.mode || 'push'
    form.rtspTransport = cfg.transport || 'auto'
  } catch {
    resetForm()
  }
  dialogVisible.value = true
}

function showDetails(row: Output) {
  currentOutput.value = row
  detailsVisible.value = true
  loadClients()
}

async function loadClients() {
  if (!currentOutput.value) return
  clientsLoading.value = true
  try {
    const { data } = await getOutputClients(currentOutput.value.id)
    clients.value = data.data || []
  } catch (error: any) {
    clients.value = []
    console.error('Failed to fetch clients:', error)
  } finally {
    clientsLoading.value = false
  }
}

function formatTime(t: string): string {
  if (!t) return '-'
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleString()
}

function buildConfig(): string {
  if (form.type === 'rtmp') {
    return JSON.stringify({ url: form.configUrl })
  } else if (form.type === 'rtsp') {
    if (form.rtspMode === 'push') {
      return JSON.stringify({ mode: 'push', url: form.configUrl, transport: 'tcp' })
    }
    return JSON.stringify({ mode: 'server', addr: form.configAddr, transport: form.rtspTransport || 'auto' })
  } else if (form.type === 'http-flv') {
    return JSON.stringify({ addr: '/live/' })
  } else if (form.type === 'webrtc') {
    return JSON.stringify({})
  }
  return '{}'
}

async function handleSubmit() {
  if (!form.name) { ElMessage.warning('请输入名称'); return }
  if (form.type === 'rtmp' && !form.configUrl) { ElMessage.warning('请输入RTMP URL'); return }
  if (form.type === 'rtsp' && form.rtspMode === 'push' && !form.configUrl) { ElMessage.warning('请输入RTSP URL'); return }
  if (form.type === 'rtsp' && form.rtspMode === 'server' && !form.configAddr) { ElMessage.warning('请输入监听地址'); return }
  if (form.type === 'http-flv' && !form.configAddr) {
    form.configAddr = '/live/'
  }

  submitting.value = true
  try {
    const config = buildConfig()
    if (isEditing.value) {
      await outputStore.updateOutput(editingId.value, form.name, form.type, config)
      ElMessage.success('修改成功')
    } else {
      await outputStore.createOutput(form.name, form.type, config)
      ElMessage.success('创建成功')
    }
    dialogVisible.value = false
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '操作失败')
  } finally {
    submitting.value = false
  }
}

async function handleStart(id: string) {
  try { await outputStore.startOutput(id); ElMessage.success('启动成功') }
  catch (e: any) { ElMessage.error(e.response?.data?.message || '启动失败') }
}

async function handleStop(id: string) {
  try { await outputStore.stopOutput(id); ElMessage.success('停止成功') }
  catch (e: any) { ElMessage.error(e.response?.data?.message || '停止失败') }
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除该输出端吗？', '确认')
    await outputStore.deleteOutput(id)
    ElMessage.success('删除成功')
  } catch (e: any) { if (e !== 'cancel') ElMessage.error(e.response?.data?.message || '删除失败') }
}
</script>

<style scoped>
.outputs-view { max-width: 1200px; margin: 0 auto; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { color: #303133; }
.stream-url { font-family: monospace; font-size: 12px; color: #606266; margin-right: 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 200px; display: inline-block; vertical-align: middle; }
.text-muted { color: #c0c4cc; }
.config-section { margin-top: 20px; }
.config-section h4 { margin: 0 0 10px; color: #303133; font-size: 14px; }
.raw-config { background: #f5f7fa; padding: 12px; border-radius: 4px; font-size: 12px; overflow-x: auto; margin: 0; }
.url-row { display: flex; align-items: center; gap: 8px; }
.url-text { font-family: monospace; font-size: 13px; }
.mono { font-family: monospace; font-size: 12px; }
.clients-actions { margin-top: 10px; text-align: right; }
</style>
