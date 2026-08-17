<template>
  <div class="inputs-view">
    <div class="page-header">
      <h2>输入端管理</h2>
      <el-button type="primary" @click="showCreateDialog">创建输入端</el-button>
    </div>

    <el-table :data="inputStore.inputs" v-loading="inputStore.loading" stripe>
      <el-table-column prop="id" label="ID" width="120" />
      <el-table-column prop="name" label="名称" />
      <el-table-column prop="type" label="类型" width="100">
        <template #default="{ row }">
          <el-tag :type="getTypeTag(row.type)">{{ row.type }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'running' ? 'success' : 'info'">
            {{ row.status === 'running' ? '运行中' : '已停止' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="350">
        <template #default="{ row }">
          <el-button
            v-if="row.type === 'file'"
            type="info"
            size="small"
            @click="showDetails(row)"
          >
            详情
          </el-button>
          <el-button
            type="primary"
            size="small"
            @click="showEditDialog(row)"
            :disabled="row.status === 'running'"
          >
            编辑
          </el-button>
          <el-button
            v-if="row.status !== 'running'"
            type="success"
            size="small"
            @click="handleStart(row.id)"
          >
            启动
          </el-button>
          <el-button
            v-if="row.status === 'running'"
            type="warning"
            size="small"
            @click="handleStop(row.id)"
          >
            停止
          </el-button>
          <el-button
            type="danger"
            size="small"
            @click="handleDelete(row.id)"
            :disabled="row.status === 'running'"
          >
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialogVisible" :title="isEditing ? '编辑输入端' : '创建输入端'" width="600">
      <el-form :model="createForm" label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="createForm.name" placeholder="请输入名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="createForm.type" placeholder="请选择类型">
            <el-option label="本地文件" value="file" />
            <el-option label="RTSP流" value="rtsp" />
          </el-select>
        </el-form-item>

        <template v-if="createForm.type === 'file'">
          <el-form-item label="文件来源">
            <el-radio-group v-model="fileSource">
              <el-radio value="upload">上传文件</el-radio>
              <el-radio value="browse">浏览服务器</el-radio>
              <el-radio value="manual">手动输入</el-radio>
            </el-radio-group>
          </el-form-item>

          <template v-if="fileSource === 'upload'">
            <el-form-item label="选择文件">
              <el-upload
                ref="uploadRef"
                :auto-upload="false"
                :limit="1"
                accept=".mp4,.flv,.ts,.avi,.mkv,.mov"
                :on-change="onUploadFileChange"
                :on-exceed="onUploadExceed"
              >
                <el-button type="primary">选择文件</el-button>
                <template #tip>
                  <div class="el-upload__tip">支持 MP4/FLV/TS/AVI/MKV/MOV</div>
                </template>
              </el-upload>
            </el-form-item>
            <el-form-item v-if="uploadFileName" label="已选文件">
              <el-tag>{{ uploadFileName }}</el-tag>
            </el-form-item>
          </template>

          <template v-if="fileSource === 'browse'">
            <el-form-item label="当前路径">
              <el-input v-model="browsePath" readonly>
                <template #prefix>
                  <span>📁</span>
                </template>
              </el-input>
            </el-form-item>
            <el-form-item label="目录内容">
              <div class="file-browser">
                <div
                  v-if="browsePath !== '/'"
                  class="file-item dir"
                  @click="browseGoUp"
                >
                  📂 ..
                </div>
                <div
                  v-for="item in browseItems"
                  :key="item.path"
                  :class="['file-item', item.is_dir ? 'dir' : 'file']"
                  @click="item.is_dir ? browseEnterDir(item.path) : browseSelectFile(item)"
                >
                  <span class="file-icon">{{ item.is_dir ? '📁' : '📄' }}</span>
                  <span class="file-name">{{ item.name }}</span>
                  <span v-if="!item.is_dir" class="file-size">{{ formatSize(item.size) }}</span>
                </div>
                <div v-if="browseItems.length === 0" class="empty">目录为空</div>
              </div>
            </el-form-item>
          </template>

          <template v-if="fileSource === 'manual'">
            <el-form-item label="文件路径">
              <el-input v-model="createForm.configPath" placeholder="/path/to/video.mp4" />
            </el-form-item>
          </template>

          <el-form-item label="循环播放">
            <el-switch v-model="createForm.configLoop" />
          </el-form-item>

          <el-form-item label="时间戳网格化">
            <div class="grid-options">
              <span class="grid-option">
                <el-switch v-model="createForm.gridVideo" size="small" />
                视频
              </span>
              <span class="grid-option">
                <el-switch v-model="createForm.gridAudio" size="small" />
                音频
              </span>
              <span class="grid-tip">将帧时间戳打平到固定间隔网格，消除源 VFR/停滞/跳变</span>
            </div>
          </el-form-item>
        </template>

        <template v-if="createForm.type === 'rtsp'">
          <el-form-item label="RTSP URL">
            <el-input v-model="createForm.configUrl" placeholder="rtsp://example.com/stream" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="createDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleCreate" :loading="creating">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailsVisible" title="媒体信息" width="700">
      <div v-loading="detailsLoading" class="media-details">
        <template v-if="currentMediaInfo">
          <div class="detail-section">
            <h4>文件信息</h4>
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="文件名">{{ currentMediaInfo.file_name }}</el-descriptions-item>
              <el-descriptions-item label="文件大小">{{ formatSize(currentMediaInfo.file_size) }}</el-descriptions-item>
              <el-descriptions-item label="格式">{{ currentMediaInfo.format_long_name }}</el-descriptions-item>
              <el-descriptions-item label="时长">{{ formatDuration(currentMediaInfo.duration) }}</el-descriptions-item>
              <el-descriptions-item label="总码率">{{ formatBitRate(currentMediaInfo.bit_rate) }}</el-descriptions-item>
              <el-descriptions-item label="文件路径" :span="2">{{ currentMediaInfo.file_path }}</el-descriptions-item>
            </el-descriptions>
          </div>

          <div v-if="videoStream" class="detail-section">
            <h4>视频流</h4>
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="编码">{{ videoStream.codec_long_name }}</el-descriptions-item>
              <el-descriptions-item label="Profile">{{ videoStream.profile }}</el-descriptions-item>
              <el-descriptions-item label="分辨率">{{ videoStream.width }}x{{ videoStream.height }}</el-descriptions-item>
              <el-descriptions-item label="像素格式">{{ videoStream.pix_fmt }}</el-descriptions-item>
              <el-descriptions-item v-if="videoStream.bit_rate" label="码率">{{ formatBitRate(parseInt(videoStream.bit_rate)) }}</el-descriptions-item>
            </el-descriptions>
          </div>

          <div v-if="audioStream" class="detail-section">
            <h4>音频流</h4>
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="编码">{{ audioStream.codec_long_name }}</el-descriptions-item>
              <el-descriptions-item label="采样率">{{ audioStream.sample_rate }} Hz</el-descriptions-item>
              <el-descriptions-item label="声道数">{{ audioStream.channels }}</el-descriptions-item>
              <el-descriptions-item v-if="audioStream.channel_layout" label="声道布局">{{ audioStream.channel_layout }}</el-descriptions-item>
              <el-descriptions-item v-if="audioStream.bit_rate" label="码率">{{ formatBitRate(parseInt(audioStream.bit_rate)) }}</el-descriptions-item>
            </el-descriptions>
          </div>

          <div v-if="currentMediaInfo.thumbnail_path" class="detail-section thumbnail-section">
            <h4>视频预览</h4>
            <el-image
              :src="'/' + currentMediaInfo.thumbnail_path"
              fit="contain"
              style="max-width: 100%; max-height: 300px; border-radius: 4px;"
            >
              <template #error>
                <div class="image-error">预览图加载失败</div>
              </template>
            </el-image>
          </div>

          <div v-if="currentMediaInfo.timestamp_stats?.length" class="detail-section">
            <h4>时间戳统计</h4>
            <div v-for="ts in currentMediaInfo.timestamp_stats" :key="ts.kind" class="ts-section">
              <div class="ts-title">
                <el-tag :type="ts.kind === 'video' ? '' : 'warning'" size="small">
                  {{ ts.kind === 'video' ? '视频' : '音频' }}
                </el-tag>
                <el-tag :type="ts.regular ? 'success' : 'danger'" size="small" style="margin-left: 8px">
                  {{ ts.regular ? '恒等间隔' : '间隔不规则' }}
                </el-tag>
              </div>
              <el-descriptions :column="3" border size="small">
                <el-descriptions-item label="帧数">{{ ts.frames }}</el-descriptions-item>
                <el-descriptions-item label="平均间隔">
                  {{ formatDelta(ts.avg_delta, ts.timescale, ts.kind) }}
                </el-descriptions-item>
                <el-descriptions-item label="中位间隔">
                  {{ formatDelta(ts.median_delta, ts.timescale, ts.kind) }}
                </el-descriptions-item>
                <el-descriptions-item label="最小/最大">
                  {{ formatDelta(ts.min_delta, ts.timescale, ts.kind) }} /
                  {{ formatDelta(ts.max_delta, ts.timescale, ts.kind) }}
                </el-descriptions-item>
                <el-descriptions-item label="抖动(标准差)">
                  {{ ts.jitter_ms.toFixed(2) }} ms
                </el-descriptions-item>
                <el-descriptions-item label="停滞/跳变">
                  {{ ts.stalls }} / {{ ts.jumps }}
                </el-descriptions-item>
              </el-descriptions>
            </div>
          </div>
        </template>
        <el-empty v-else-if="!detailsLoading" description="暂无媒体信息" />
      </div>
      <template #footer>
        <el-button @click="handleReProbe" :loading="probing">重新探测</el-button>
        <el-button @click="detailsVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox, type UploadFile } from 'element-plus'
import { useInputStore } from '@/stores/input'
import { listFiles, uploadFile, probeInput, type FileEntry, type MediaInfo, type StreamInfo } from '@/api'

const inputStore = useInputStore()
const createDialogVisible = ref(false)
const creating = ref(false)
const isEditing = ref(false)
const editingId = ref('')

const createForm = reactive({
  name: '',
  type: 'file',
  configPath: '',
  configUrl: '',
  configLoop: true,
  gridVideo: false,
  gridAudio: false,
})

const fileSource = ref<'upload' | 'browse' | 'manual'>('browse')
const uploadRef = ref()
const uploadFileName = ref('')
const uploadFileObj = ref<File | null>(null)

const browsePath = ref('/')
const browseItems = ref<FileEntry[]>([])

const detailsVisible = ref(false)
const detailsLoading = ref(false)
const probing = ref(false)
const currentInputId = ref('')
const currentMediaInfo = ref<MediaInfo | null>(null)

const videoStream = computed<StreamInfo | null>(() => {
  if (!currentMediaInfo.value) return null
  return currentMediaInfo.value.streams.find(s => s.codec_type === 'video') || null
})

const audioStream = computed<StreamInfo | null>(() => {
  if (!currentMediaInfo.value) return null
  return currentMediaInfo.value.streams.find(s => s.codec_type === 'audio') || null
})

onMounted(() => {
  inputStore.fetchInputs()
})

watch(fileSource, (val) => {
  if (val === 'browse') {
    loadBreadDir('/')
  }
})

function getTypeTag(type: string) {
  const tags: Record<string, string> = {
    file: '',
    rtsp: 'warning',
    rtmp: 'danger',
    hls: 'success',
  }
  return tags[type] || 'info'
}

function showCreateDialog() {
  isEditing.value = false
  editingId.value = ''
  createForm.name = ''
  createForm.type = 'file'
  createForm.configPath = ''
  createForm.configUrl = ''
  createForm.configLoop = true
  createForm.gridVideo = false
  createForm.gridAudio = false
  fileSource.value = 'browse'
  uploadFileName.value = ''
  uploadFileObj.value = null
  if (uploadRef.value) {
    uploadRef.value.clearFiles()
  }
  browsePath.value = '/'
  browseItems.value = []
  loadBreadDir('/')
  createDialogVisible.value = true
}

function showEditDialog(row: any) {
  isEditing.value = true
  editingId.value = row.id
  createForm.name = row.name
  createForm.type = row.type
  createForm.configPath = ''
  createForm.configUrl = ''
  createForm.configLoop = true
  createForm.gridVideo = false
  createForm.gridAudio = false
  fileSource.value = 'manual'

  if (row.type === 'file') {
    try {
      const cfg = JSON.parse(row.config)
      createForm.configPath = cfg.path || ''
      createForm.configLoop = cfg.loop ?? true
      createForm.gridVideo = !!cfg.timestamp_grid?.video
      createForm.gridAudio = !!cfg.timestamp_grid?.audio
    } catch {}
  } else if (row.type === 'rtsp') {
    try {
      const cfg = JSON.parse(row.config)
      createForm.configUrl = cfg.url || ''
    } catch {}
  }

  createDialogVisible.value = true
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatDuration(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

function formatBitRate(bps: number): string {
  if (bps >= 1000000) return (bps / 1000000).toFixed(2) + ' Mbps'
  return (bps / 1000).toFixed(0) + ' Kbps'
}

function formatDelta(delta: number, timescale: number, kind: string): string {
  if (!delta || !timescale) return '-'
  if (kind === 'audio') {
    // 音频以 采样数/帧 展示更直观；同时给 ms
    const ms = (delta / timescale) * 1000
    return `${delta}采样(${ms.toFixed(2)}ms)`
  }
  const ms = (delta / timescale) * 1000
  const fps = timescale / delta
  return `${ms.toFixed(2)}ms(${fps.toFixed(1)}fps)`
}

async function loadBreadDir(path: string) {
  try {
    const { data } = await listFiles(path)
    browseItems.value = data.data || []
    browsePath.value = path
  } catch (e: any) {
    ElMessage.error(e.response?.data?.message || '读取目录失败')
  }
}

function browseGoUp() {
  const parts = browsePath.value.split('/').filter(Boolean)
  parts.pop()
  loadBreadDir('/' + parts.join('/'))
}

function browseEnterDir(path: string) {
  loadBreadDir(path)
}

function browseSelectFile(item: FileEntry) {
  createForm.configPath = item.path
  ElMessage.success('已选择: ' + item.name)
}

function onUploadFileChange(file: UploadFile) {
  if (file.raw) {
    uploadFileName.value = file.name
    uploadFileObj.value = file.raw
  }
}

function onUploadExceed() {
  ElMessage.warning('只能选择一个文件')
}

async function handleCreate() {
  if (!createForm.name) {
    ElMessage.warning('请输入名称')
    return
  }

  let config = ''

  if (createForm.type === 'file') {
    if (fileSource.value === 'upload') {
      if (!uploadFileObj.value) {
        ElMessage.warning('请选择要上传的文件')
        return
      }
      creating.value = true
      try {
        const { data } = await uploadFile(uploadFileObj.value)
        createForm.configPath = data.data.path
      } catch (e: any) {
        ElMessage.error(e.response?.data?.message || '上传失败')
        creating.value = false
        return
      }
    } else if (fileSource.value === 'browse') {
      if (!createForm.configPath) {
        ElMessage.warning('请从文件浏览器中选择一个文件')
        return
      }
    } else {
      if (!createForm.configPath) {
        ElMessage.warning('请输入文件路径')
        return
      }
    }
    config = JSON.stringify({
      path: createForm.configPath,
      loop: createForm.configLoop,
      timestamp_grid: {
        video: createForm.gridVideo,
        audio: createForm.gridAudio,
      },
    })
  } else if (createForm.type === 'rtsp') {
    if (!createForm.configUrl) {
      ElMessage.warning('请输入RTSP URL')
      return
    }
    config = JSON.stringify({ url: createForm.configUrl, transport: 'tcp' })
  }

  try {
    if (isEditing.value) {
      await inputStore.updateInput(editingId.value, createForm.name, createForm.type, config)
      ElMessage.success('修改成功')
    } else {
      await inputStore.createInput(createForm.name, createForm.type, config)
      ElMessage.success('创建成功')
    }
    createDialogVisible.value = false
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '创建失败')
  } finally {
    creating.value = false
  }
}

async function showDetails(row: any) {
  currentInputId.value = row.id
  detailsVisible.value = true
  currentMediaInfo.value = null

  if (row.media_info) {
    try {
      currentMediaInfo.value = JSON.parse(row.media_info)
    } catch {
      await fetchProbe()
    }
  } else {
    await fetchProbe()
  }
}

async function fetchProbe() {
  detailsLoading.value = true
  try {
    const { data } = await probeInput(currentInputId.value)
    currentMediaInfo.value = data.data
  } catch (e: any) {
    ElMessage.error(e.response?.data?.message || '探测失败')
  } finally {
    detailsLoading.value = false
  }
}

async function handleReProbe() {
  probing.value = true
  try {
    await fetchProbe()
    ElMessage.success('探测完成')
  } finally {
    probing.value = false
  }
}

async function handleStart(id: string) {
  try {
    await inputStore.startInput(id)
    ElMessage.success('启动成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '启动失败')
  }
}

async function handleStop(id: string) {
  try {
    await inputStore.stopInput(id)
    ElMessage.success('停止成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '停止失败')
  }
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除该输入端吗？', '确认')
    await inputStore.deleteInput(id)
    ElMessage.success('删除成功')
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || '删除失败')
    }
  }
}
</script>

<style scoped>
.inputs-view {
  max-width: 1200px;
  margin: 0 auto;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.page-header h2 {
  color: #303133;
}

.file-browser {
  border: 1px solid #dcdfe6;
  border-radius: 4px;
  max-height: 300px;
  overflow-y: auto;
  width: 100%;
}

.file-item {
  display: flex;
  align-items: center;
  padding: 8px 12px;
  cursor: pointer;
  border-bottom: 1px solid #f5f5f5;
}

.file-item:last-child {
  border-bottom: none;
}

.file-item:hover {
  background: #f5f7fa;
}

.file-item.dir {
  font-weight: 600;
}

.file-icon {
  margin-right: 8px;
  flex-shrink: 0;
}

.file-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-size {
  color: #909399;
  font-size: 12px;
  margin-left: 8px;
  flex-shrink: 0;
}

.empty {
  text-align: center;
  color: #909399;
  padding: 20px;
}

.media-details {
  min-height: 100px;
}

.detail-section {
  margin-bottom: 20px;
}

.detail-section h4 {
  margin: 0 0 10px 0;
  color: #303133;
  font-size: 14px;
}

.thumbnail-section {
  text-align: center;
}

.image-error {
  color: #909399;
  font-size: 14px;
}

.grid-options {
  display: flex;
  align-items: center;
  gap: 16px;
}

.grid-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #303133;
}

.grid-tip {
  color: #909399;
  font-size: 12px;
}

.ts-section {
  margin-bottom: 12px;
}

.ts-title {
  margin-bottom: 6px;
}
</style>
