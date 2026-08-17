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
      </el-form>
    </el-card>

    <!-- WebRTC 测试结果 -->
    <el-card v-if="protocol === 'webrtc'" class="result-card">
      <template #header>
        <div class="card-header">
          <span>WebRTC 浏览器拉流测试</span>
          <el-tag v-if="wrStatus" :type="wrStatus === 'connected' ? 'success' : wrStatus === 'failed' ? 'danger' : 'info'">
            {{ wrStatusLabel }}
          </el-tag>
        </div>
      </template>

      <div v-if="wrConnecting" v-loading="true" class="wr-loading">正在建立 WebRTC 连接...</div>

      <template v-if="wrConnected">
        <video ref="videoEl" autoplay muted playsinline class="wr-video" controls />
        <div class="wr-stats">
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="状态">
              <el-tag type="success">已连接</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="收到 RTP 包">
              <span class="mono">{{ rtpPackets }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="码率">
              <span class="mono">{{ bitrate }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="分辨率">
              <span class="mono">{{ resolution }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="帧率">
              <span class="mono">{{ frameRate }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="时长">
              <span class="mono">{{ wrDuration }}</span>
            </el-descriptions-item>
          </el-descriptions>
        </div>
      </template>

      <el-alert v-if="wrError" type="error" :title="wrError" show-icon :closable="false" />
    </el-card>

    <!-- 后端探测结果 (RTMP/HTTP-FLV) -->
    <el-card v-else class="result-card">
      <template #header>
        <div class="card-header">
          <span>连通性测试结果</span>
          <el-tag v-if="testResult" :type="testResult.ok ? 'success' : 'danger'">
            {{ testResult.ok ? '成功' : '失败' }}
          </el-tag>
        </div>
      </template>
      <div v-if="testResult" class="test-result-detail">
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="URL">{{ testResult.url }}</el-descriptions-item>
          <el-descriptions-item label="耗时">{{ testResult.latency_ms }} ms</el-descriptions-item>
          <el-descriptions-item label="结果">{{ testResult.detail }}</el-descriptions-item>
        </el-descriptions>
      </div>
      <el-empty v-else description="点击 测试 按钮开始" />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getOutputs, testStreamURL } from '@/api'
import type { Output, StreamTestResult } from '@/api'

const protocol = ref<'webrtc' | 'http-flv'>('webrtc')
const url = ref('')
const outputs = ref<Output[]>([])
const selectedOutput = ref('')
const testing = ref(false)
const testResult = ref<StreamTestResult | null>(null)

// WebRTC 状态
const videoEl = ref<HTMLVideoElement>()
let pc: RTCPeerConnection | null = null
let statsTimer: number | null = null
const wrConnecting = ref(false)
const wrConnected = ref(false)
const wrStatus = ref('')
const wrError = ref('')
const rtpPackets = ref(0)
const bitrate = ref('')
const resolution = ref('')
const frameRate = ref('')
const wrDuration = ref('')
let lastBytes = 0
let lastTime = 0
let packets = 0
let firstFrameTime = 0

const wrStatusLabel = computed(() => {
  const map: Record<string, string> = {
    connecting: '连接中',
    connected: '已连接',
    failed: '失败',
    disconnected: '已断开',
    closed: '已关闭',
  }
  return map[wrStatus.value] || wrStatus.value
})

onBeforeUnmount(() => {
  stopWebRTC()
})

async function loadOutputs() {
  try {
    const { data } = await getOutputs()
    outputs.value = data.data || []
  } catch {
    // ignore
  }
}
loadOutputs()

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
  testResult.value = null
  wrError.value = ''

  if (protocol.value === 'webrtc') {
    startWebRTC()
  } else {
    testing.value = true
    try {
      const { data } = await testStreamURL(url.value)
      testResult.value = data.data
    } catch (e: any) {
      testResult.value = {
        url: url.value,
        ok: false,
        latency_ms: 0,
        detail: e.response?.data?.message || '请求失败',
      }
    } finally {
      testing.value = false
    }
  }
}

// ---------- WebRTC 浏览器测试 ----------
async function startWebRTC() {
  stopWebRTC()
  wrConnecting.value = true
  wrStatus.value = 'connecting'
  wrConnected.value = false
  packets = 0
  lastBytes = 0
  lastTime = 0
  firstFrameTime = 0
  rtpPackets.value = 0
  bitrate.value = ''
  resolution.value = ''
  frameRate.value = ''
  wrDuration.value = ''

  try {
    pc = new RTCPeerConnection()
    pc.addTransceiver('video', { direction: 'recvonly' })

    pc.ontrack = (ev) => {
      if (videoEl.value) {
        videoEl.value.srcObject = ev.streams[0]
      }
      ev.streams[0].getVideoTracks().forEach((track) => {
        const settings = track.getSettings()
        if (settings.width) resolution.value = `${settings.width}x${settings.height}`
        if (settings.frameRate) frameRate.value = `${Math.round(settings.frameRate)} fps`
      })
    }

    pc.onconnectionstatechange = () => {
      const st = pc?.connectionState
      wrStatus.value = st || ''
      if (st === 'connected') {
        wrConnected.value = true
        wrConnecting.value = false
        startStatsTimer()
      } else if (st === 'failed' || st === 'disconnected' || st === 'closed') {
        wrConnecting.value = false
        wrConnected.value = false
        if (st === 'failed') wrError.value = 'WebRTC 连接失败（网络/信令问题）'
        stopStatsTimer()
      }
    }

    // WHEP 信令
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    const resp = await fetch(url.value, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: offer.sdp,
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`WHEP 信令失败 (${resp.status}): ${text.slice(0, 200)}`)
    }
    const answer = await resp.text()
    await pc.setRemoteDescription({ type: 'answer', sdp: answer })
  } catch (e: any) {
    wrConnecting.value = false
    wrStatus.value = 'failed'
    wrError.value = e.message || 'WebRTC 测试失败'
    stopWebRTC()
  }
}

function startStatsTimer() {
  lastTime = performance.now()
  lastBytes = 0
  statsTimer = window.setInterval(async () => {
    if (!pc) return
    try {
      const stats = await pc.getStats()
      const now = performance.now()
      stats.forEach((s) => {
        if (s.type === 'inbound-rtp' && s.kind === 'video') {
          rtpPackets.value = s.packetsReceived || 0
          if (lastBytes > 0) {
            const bytesDelta = (s.bytesReceived || 0) - lastBytes
            const dt = (now - lastTime) / 1000
            if (dt > 0) bitrate.value = formatBitrate((bytesDelta * 8) / dt)
          }
          lastBytes = s.bytesReceived || 0
          lastTime = now
          if (!firstFrameTime && s.packetsReceived > 0) firstFrameTime = now
          if (firstFrameTime) wrDuration.value = `${((now - firstFrameTime) / 1000).toFixed(1)} s`
        }
      })
    } catch {}
  }, 1000)
}

function stopStatsTimer() {
  if (statsTimer !== null) {
    clearInterval(statsTimer)
    statsTimer = null
  }
}

function formatBitrate(bps: number): string {
  if (bps > 1e6) return `${(bps / 1e6).toFixed(2)} Mbps`
  if (bps > 1e3) return `${(bps / 1e3).toFixed(1)} kbps`
  return `${Math.round(bps)} bps`
}

function stopWebRTC() {
  stopStatsTimer()
  if (pc) {
    pc.ontrack = null
    pc.onconnectionstatechange = null
    try {
      pc.close()
    } catch {}
    pc = null
  }
  if (videoEl.value) {
    videoEl.value.srcObject = null
  }
  wrConnected.value = false
  wrConnecting.value = false
}
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
.test-result-detail { margin-top: 8px; }
</style>
