<template>
  <div class="test-view">
    <div class="page-header">
      <h2>流地址测试</h2>
    </div>

    <el-card class="test-card">
      <el-form label-width="100px">
        <el-form-item label="协议类型">
          <el-radio-group v-model="protocol">
            <el-radio-button value="webrtc">WebRTC</el-radio-button>
            <el-radio-button value="http-flv">HTTP-FLV</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <el-form-item :label="protocol === 'webrtc' ? 'WHEP URL' : '流 URL'">
          <el-input v-model="url" placeholder="请输入要测试的流地址">
            <template #append>
              <el-button :loading="testing" @click="startTest">测试</el-button>
            </template>
          </el-input>
        </el-form-item>

        <el-form-item v-if="outputs.length > 0" label="现有输出">
          <el-select v-model="selectedOutput" placeholder="从现有输出填充 URL" @change="fillFromOutput" clearable>
            <el-option
              v-for="o in outputs"
              :key="o.id"
              :label="`${o.name} (${o.type})`"
              :value="o.id"
            />
          </el-select>
        </el-form-item>

        <el-form-item v-if="isTesting" label="测试状态">
          <el-tag type="success">测试进行中（切换页面不中断）</el-tag>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- WebRTC 测试结果 -->
    <el-card v-if="protocol === 'webrtc'" class="result-card">
      <template #header>
        <div class="card-header">
          <span>WebRTC 浏览器拉流测试</span>
          <el-tag v-if="store.wrStatus" :type="store.wrStatus === 'connected' ? 'success' : store.wrStatus === 'failed' ? 'danger' : 'info'">
            {{ wrStatusLabel }}
          </el-tag>
        </div>
      </template>

      <div v-if="store.wrConnecting" v-loading="true" class="wr-loading">正在建立 WebRTC 连接...</div>

      <video v-show="store.wrConnected" ref="videoEl" autoplay muted playsinline class="wr-video" controls />

      <template v-if="store.wrConnected">
        <div class="wr-stats">
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="状态">
              <el-tag type="success">已连接</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="收到 RTP 包">
              <span class="mono">{{ store.rtpPackets }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="码率">
              <span class="mono">{{ store.bitrate }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="分辨率">
              <span class="mono">{{ store.resolution }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="帧率">
              <span class="mono">{{ store.frameRate }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="时长">
              <span class="mono">{{ store.wrDuration }}</span>
            </el-descriptions-item>
          </el-descriptions>
        </div>
      </template>

      <el-alert v-if="store.wrError" type="error" :title="store.wrError" show-icon :closable="false" />
    </el-card>

    <!-- HTTP-FLV 测试结果 -->
    <el-card v-else class="result-card">
      <template #header>
        <div class="card-header">
          <span>HTTP-FLV 播放测试</span>
          <el-tag v-if="store.flvStatus" :type="store.flvStatus === 'playing' ? 'success' : store.flvStatus === 'failed' ? 'danger' : 'info'">
            {{ flvStatusLabel }}
          </el-tag>
        </div>
      </template>

      <div v-if="store.flvConnecting" v-loading="true" class="wr-loading">正在拉流...</div>

      <video v-show="store.flvPlaying" ref="flvVideoEl" autoplay muted controls class="wr-video" />

      <template v-if="store.flvPlaying">
        <div class="wr-stats">
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="状态">
              <el-tag type="success">播放中</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="分辨率">
              <span class="mono">{{ store.flvResolution }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="播放时长">
              <span class="mono">{{ store.flvDuration }}</span>
            </el-descriptions-item>
          </el-descriptions>
        </div>
      </template>

      <el-alert v-if="store.flvError" type="error" :title="store.flvError" show-icon :closable="false" />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { getOutputs } from '@/api'
import { useStreamTestStore } from '@/stores/streamTest'
import type { Output } from '@/api'

const store = useStreamTestStore()
const protocol = ref<'webrtc' | 'http-flv'>('webrtc')
const url = ref('')
const outputs = ref<Output[]>([])
const selectedOutput = ref('')
const testing = ref(false)
const videoEl = ref<HTMLVideoElement>()
const flvVideoEl = ref<HTMLVideoElement>()

const wrStatusLabel = computed(() => {
  const map: Record<string, string> = {
    connecting: '连接中',
    connected: '已连接',
    failed: '失败',
    disconnected: '已断开',
    closed: '已关闭',
  }
  return map[store.wrStatus] || store.wrStatus
})

const flvStatusLabel = computed(() => {
  const map: Record<string, string> = {
    connecting: '连接中',
    playing: '播放中',
    failed: '失败',
  }
  return map[store.flvStatus] || store.flvStatus
})

const isTesting = computed(
  () => store.wrConnected || store.wrConnecting || store.flvPlaying || store.flvConnecting,
)

onMounted(async () => {
  await loadOutputs()
  // 重挂载时重新 attach 已建立的媒体流（切 Tab 回来）
  await nextTick()
  if (videoEl.value) store.attachWebRTCVideo(videoEl.value)
  if (flvVideoEl.value) store.attachFLVVideo(flvVideoEl.value)
})

// 切 Tab 不停止测试；仅当组件内主动停止时才调用 store 的 stop
onBeforeUnmount(() => {
  // 不做任何事：播放器/连接由 store 保持，切 Tab 继续
})

async function loadOutputs() {
  try {
    const { data } = await getOutputs()
    outputs.value = data.data || []
  } catch {
    // ignore
  }
}

function outputUrl(o: Output): string {
  try {
    const cfg = JSON.parse(o.config)
    const host = location.hostname
    if (o.type === 'rtsp') {
      const port = (cfg.addr || ':5544').replace(':', '')
      return `rtsp://${host}:${port}/live/${o.id}`
    }
    if (o.type === 'http-flv') {
      return `http://${location.host}/live/${o.id}.flv`
    }
    if (o.type === 'webrtc') {
      return `http://${location.host}/live/${o.id}/whep`
    }
  } catch {}
  return ''
}

function fillFromOutput(id: string) {
  const o = outputs.value.find((x) => x.id === id)
  if (!o) return
  const u = outputUrl(o)
  if (!u) {
    ElMessage.warning('该输出类型暂不支持测试')
    return
  }
  url.value = u
  if (o.type === 'webrtc') protocol.value = 'webrtc'
  else if (o.type === 'http-flv') protocol.value = 'http-flv'
}

async function startTest() {
  if (!url.value) {
    ElMessage.warning('请输入 URL')
    return
  }
  testing.value = true
  try {
    if (protocol.value === 'webrtc') {
      await store.startWebRTC(url.value)
    } else {
      await nextTick()
      await store.startFLV(url.value, flvVideoEl.value)
    }
  } finally {
    testing.value = false
  }
}

// 监听播放状态变化，确保 video 元素就绪后 attach
watch(
  () => store.wrConnected,
  async (connected) => {
    if (connected) {
      await nextTick()
      if (videoEl.value) store.attachWebRTCVideo(videoEl.value)
    }
  },
)
watch(
  () => store.flvPlaying,
  async (playing) => {
    if (playing) {
      await nextTick()
      if (flvVideoEl.value) store.attachFLVVideo(flvVideoEl.value)
    }
  },
)
</script>

<style scoped>
.test-view { max-width: 900px; margin: 0 auto; }
.page-header { margin-bottom: 20px; }
.page-header h2 { color: #303133; }
.test-card { margin-bottom: 20px; }
.result-card { margin-bottom: 20px; }
.card-header { display: flex; justify-content: space-between; align-items: center; }
.mono { font-family: monospace; font-size: 12px; }
.wr-video { width: 100%; max-height: 400px; background: #000; margin-bottom: 12px; border-radius: 4px; }
.wr-loading { height: 120px; display: flex; align-items: center; justify-content: center; }
.wr-stats { margin-top: 8px; }
</style>
