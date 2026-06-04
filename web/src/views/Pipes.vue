<template>
  <div class="pipes-view">
    <div class="page-header">
      <h2>管道管理</h2>
      <el-button type="primary" @click="showCreateDialog">创建管道</el-button>
    </div>

    <el-table :data="pipeStore.pipes" v-loading="pipeStore.loading" stripe>
      <el-table-column prop="id" label="ID" width="120" />
      <el-table-column label="输入端" min-width="150">
        <template #default="{ row }">
          {{ getInputName(row.input_id) }}
        </template>
      </el-table-column>
      <el-table-column label="输出端" min-width="150">
        <template #default="{ row }">
          {{ getOutputName(row.output_id) }}
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

    <el-dialog v-model="dialogVisible" :title="isEditing ? '编辑管道' : '创建管道'" width="600">
      <el-form :model="form" label-width="100px">
        <el-form-item label="输入端">
          <el-select v-model="form.inputId" placeholder="请选择输入端" @change="onInputChange">
            <el-option v-for="input in inputStore.inputs" :key="input.id" :label="input.name" :value="input.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="输出端">
          <el-select v-model="form.outputId" placeholder="请选择输出端">
            <el-option v-for="output in outputStore.outputs" :key="output.id" :label="output.name" :value="output.id" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="inputStreams.length > 0" label="通道映射">
          <div class="channel-map">
            <div v-for="stream in inputStreams" :key="stream.channel_id" class="channel-item">
              <el-checkbox v-model="stream.selected" :label="stream.kind + ' - ' + stream.codec_name" />
              <span class="channel-info">{{ stream.kind === 'video' ? stream.width + 'x' + stream.height : stream.sample_rate + 'Hz' }}</span>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="音视频同步">
          <el-switch v-model="form.muxSync" />
          <span class="form-hint">默认关闭，输出端一般不解码</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSubmit" :loading="submitting">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailsVisible" title="管道详情" width="600">
      <div v-if="currentPipe">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="ID">{{ currentPipe.id }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="currentPipe.status === 'running' ? 'success' : 'info'">
              {{ currentPipe.status === 'running' ? '运行中' : '已停止' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="输入端">{{ getInputName(currentPipe.input_id) }}</el-descriptions-item>
          <el-descriptions-item label="输出端">{{ getOutputName(currentPipe.output_id) }}</el-descriptions-item>
          <el-descriptions-item label="输入端ID">{{ currentPipe.input_id }}</el-descriptions-item>
          <el-descriptions-item label="输出端ID">{{ currentPipe.output_id }}</el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ currentPipe.created_at }}</el-descriptions-item>
          <el-descriptions-item label="更新时间">{{ currentPipe.updated_at }}</el-descriptions-item>
        </el-descriptions>

        <div v-if="inputConfig" class="config-section">
          <h4>输入端配置</h4>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item v-if="inputConfig.path" label="文件路径">{{ inputConfig.path }}</el-descriptions-item>
            <el-descriptions-item v-if="inputConfig.url" label="流地址">
              <div class="url-row">
                <span class="url-text">{{ inputConfig.url }}</span>
                <el-button link type="primary" size="small" @click="copyUrl(inputConfig.url)">复制</el-button>
              </div>
            </el-descriptions-item>
            <el-descriptions-item v-if="inputConfig.transport" label="传输协议">{{ inputConfig.transport }}</el-descriptions-item>
          </el-descriptions>
        </div>

        <div v-if="outputConfig" class="config-section">
          <h4>输出端配置</h4>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item v-if="outputConfig.url" label="推流地址">
              <div class="url-row">
                <span class="url-text">{{ outputConfig.url }}</span>
                <el-button link type="primary" size="small" @click="copyUrl(outputConfig.url)">复制</el-button>
              </div>
            </el-descriptions-item>
            <el-descriptions-item v-if="outputConfig.mode" label="模式">{{ outputConfig.mode === 'push' ? '推流' : '服务' }}</el-descriptions-item>
            <el-descriptions-item v-if="outputConfig.addr" label="监听地址">{{ outputConfig.addr }}</el-descriptions-item>
          </el-descriptions>
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
import { usePipeStore } from '@/stores/pipe'
import { useInputStore } from '@/stores/input'
import { useOutputStore } from '@/stores/output'
import * as api from '@/api'
import type { Pipe } from '@/api'

const pipeStore = usePipeStore()
const inputStore = useInputStore()
const outputStore = useOutputStore()
const dialogVisible = ref(false)
const detailsVisible = ref(false)
const isEditing = ref(false)
const editingId = ref('')
const submitting = ref(false)
const currentPipe = ref<Pipe | null>(null)

const form = reactive({ inputId: '', outputId: '', muxSync: false })
const inputStreams = ref<any[]>([])

const inputConfig = computed(() => {
  if (!currentPipe.value) return null
  const input = inputStore.inputs.find(i => i.id === currentPipe.value!.input_id)
  if (!input) return null
  try { return JSON.parse(input.config) } catch { return null }
})

const outputConfig = computed(() => {
  if (!currentPipe.value) return null
  const output = outputStore.outputs.find(o => o.id === currentPipe.value!.output_id)
  if (!output) return null
  try { return JSON.parse(output.config) } catch { return null }
})

onMounted(() => {
  pipeStore.fetchPipes()
  inputStore.fetchInputs()
  outputStore.fetchOutputs()
})

function getInputName(id: string): string {
  const input = inputStore.inputs.find(i => i.id === id)
  return input ? input.name : id
}

function getOutputName(id: string): string {
  const output = outputStore.outputs.find(o => o.id === id)
  return output ? output.name : id
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

function showCreateDialog() {
  isEditing.value = false
  editingId.value = ''
  form.inputId = ''
  form.outputId = ''
  form.muxSync = false
  inputStreams.value = []
  dialogVisible.value = true
}

function showEditDialog(row: Pipe) {
  isEditing.value = true
  editingId.value = row.id
  form.inputId = row.input_id
  form.outputId = row.output_id
  form.muxSync = (row as any).mux_sync || false
  inputStreams.value = []
  dialogVisible.value = true
}

async function onInputChange(inputId: string) {
  if (!inputId) {
    inputStreams.value = []
    return
  }
  try {
    const { data } = await api.probeInput(inputId)
    if (data.data && data.data.streams) {
      inputStreams.value = data.data.streams.map((s: any) => ({
        ...s,
        selected: true,
      }))
    }
  } catch {
    inputStreams.value = []
  }
}

function showDetails(row: Pipe) {
  currentPipe.value = row
  detailsVisible.value = true
}

async function handleSubmit() {
  if (!form.inputId) { ElMessage.warning('请选择输入端'); return }
  if (!form.outputId) { ElMessage.warning('请选择输出端'); return }

  const selectedChannels = inputStreams.value
    .filter(s => s.selected)
    .map(s => ({ input_channel_id: s.channel_id, output_channel_id: s.channel_id }))

  const channelMap = selectedChannels.length > 0 ? JSON.stringify(selectedChannels) : undefined

  submitting.value = true
  try {
    if (isEditing.value) {
      await pipeStore.updatePipe(editingId.value, form.inputId, form.outputId, channelMap, form.muxSync)
      ElMessage.success('修改成功')
    } else {
      await pipeStore.createPipe(form.inputId, form.outputId, channelMap, form.muxSync)
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
  try { await pipeStore.startPipe(id); ElMessage.success('启动成功') }
  catch (e: any) { ElMessage.error(e.response?.data?.message || '启动失败') }
}

async function handleStop(id: string) {
  try { await pipeStore.stopPipe(id); ElMessage.success('停止成功') }
  catch (e: any) { ElMessage.error(e.response?.data?.message || '停止失败') }
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除该管道吗？', '确认')
    await pipeStore.deletePipe(id)
    ElMessage.success('删除成功')
  } catch (e: any) { if (e !== 'cancel') ElMessage.error(e.response?.data?.message || '删除失败') }
}
</script>

<style scoped>
.pipes-view { max-width: 1200px; margin: 0 auto; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { color: #303133; }
.config-section { margin-top: 20px; }
.config-section h4 { margin: 0 0 10px; color: #303133; font-size: 14px; }
.url-row { display: flex; align-items: center; gap: 8px; }
.url-text { font-family: monospace; font-size: 13px; }
.channel-map { border: 1px solid #dcdfe6; border-radius: 4px; padding: 10px; width: 100%; }
.channel-item { display: flex; align-items: center; padding: 6px 0; }
.channel-info { margin-left: 10px; color: #909399; font-size: 12px; }
.form-hint { margin-left: 10px; color: #909399; font-size: 12px; }
</style>
